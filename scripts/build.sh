#!/usr/bin/env bash
# Run from any directory. Never deploy a build that failed validation.
set -Eeuo pipefail

usage() {
  cat <<'HELP'
Usage: scripts/build.sh [--deploy] [--help]

Check formatting, modules, vet, Staticcheck, race/debug tests (on the host),
release/debug builds, binary vulnerabilities, and isolated CLI smoke tests.
--deploy atomically installs the validated release executable.

Environment:
  BINDIR        Install directory (default: ${PREFIX:-$HOME/.local}/bin)
  GOTOOLCHAIN   Go toolchain (default: go1.26.8)
  VERSION       Override version (default: git describe, including dirty state)

Logs: bin/build-logs/ (one log per step, plus summary.txt)
Pinned analysis tools may download modules on the first run.
HELP
}

deploy=false
for arg in "$@"; do
  case "$arg" in
    --deploy) deploy=true ;;
    --help|-h) usage; exit 0 ;;
    *) printf 'Unknown argument: %s\n' "$arg" >&2; usage >&2; exit 2 ;;
  esac
done

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
for dependency in go git python3 sqlite3 timeout install; do
  command -v "$dependency" >/dev/null || { printf 'Missing dependency: %s\n' "$dependency" >&2; exit 1; }
done
export GOTOOLCHAIN=${GOTOOLCHAIN:-go1.26.8}
log_dir="$root/bin/build-logs"
mkdir -p "$log_dir"
summary="$log_dir/summary.txt"
printf 'Build started: %s\n' "$(date -u +%FT%TZ)" > "$summary"
step=preflight
work=$(mktemp -d "$root/bin/.build-XXXXXX")
install_tmp=
cleanup() {
  rm -rf -- "$work"
  if [[ -n "$install_tmp" ]]; then rm -f -- "$install_tmp"; fi
}
trap cleanup EXIT
trap 'code=$?; printf "FAIL: %s (exit %s). Logs: %s\n" "$step" "$code" "$log_dir" | tee -a "$summary" >&2; exit "$code"' ERR

run() {
  step=$1; shift
  printf '\n== %s ==\n' "$step"
  "$@" 2>&1 | tee "$log_dir/$step.log"
  printf 'PASS: %s\n' "$step" >> "$summary"
}

check_format() {
  # go may be a container wrapper; compile tools there and execute on the host.
  local goroot unformatted
  goroot=$(go env GOROOT)
  if [[ ! -x "$goroot/bin/gofmt" ]]; then
    printf 'Cannot access gofmt at %s/bin/gofmt\n' "$goroot" >&2; return 1
  fi
  unformatted=$("$goroot/bin/gofmt" -l cmd internal)
  if [[ -n "$unformatted" ]]; then
    printf '%s\nRun make fmt, then retry.\n' "$unformatted" >&2; return 1
  fi
}

host_tests() {
  local mode=$1 package index=0
  local -a flags=(-race)
  if [[ "$mode" == debug ]]; then flags+=(-tags debug); fi
  go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./... > "$work/packages"
  while IFS= read -r package; do
    [[ -n "$package" ]] || continue
    index=$((index + 1))
    go test "${flags[@]}" -c -o "$work/test-$mode-$index" "$package" < /dev/null
    printf '\nPackage: %s\n' "$package" < /dev/null
    timeout 180 "$work/test-$mode-$index" -test.v -test.timeout=2m | tee "$work/test.log"
    if grep -q -- '--- SKIP:' "$work/test.log"; then
      printf 'Skipped tests are not allowed in the build gate. Install their dependencies.\n' >&2
      return 1
    fi
  done < "$work/packages"
  if [[ "$index" == 0 ]]; then printf 'No test packages discovered.\n' >&2; return 1; fi
}

version=${VERSION:-$(git describe --always --dirty)}
if [[ ! "$version" =~ ^[a-zA-Z0-9._+-]+$ ]]; then
  printf 'VERSION must contain only letters, numbers, dot, underscore, plus or hyphen.\n' >&2; exit 2
fi
run toolchain go version
run formatting check_format
run whitespace git diff --check
run modules go mod verify
run vet go vet ./...
run staticcheck go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...
run race-tests host_tests release
run debug-tests host_tests debug
run release-build go build -trimpath -ldflags "-s -w -X agentcli.local/ai/internal/cli.Version=$version" -o "$work/ai" ./cmd/ai
run debug-build go build -trimpath -tags debug -ldflags "-X agentcli.local/ai/internal/cli.Version=$version-debug" -o "$work/ai-debug" ./cmd/ai
run vulnerabilities go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 -mode=binary "$work/ai"
run secrets go run github.com/zricethezav/gitleaks/v8@v8.24.0 detect --source . -v
run smoke python3 scripts/smoke.py "$work/ai" "$work/ai-debug"
run shell-syntax bash -n scripts/build.sh

# Promote only verified artifacts. Staging under bin keeps renames on one filesystem.
mv -f -- "$work/ai" "$root/bin/ai"
mv -f -- "$work/ai-debug" "$root/bin/ai-debug"
if "$deploy"; then
  step=deploy
  bindir=${BINDIR:-${PREFIX:-$HOME/.local}/bin}
  mkdir -p -- "$bindir"
  install_tmp=$(mktemp "$bindir/.ai-install-XXXXXX")
  install -m 0755 "$root/bin/ai" "$install_tmp"
  "$install_tmp" --version
  mv -f -- "$install_tmp" "$bindir/ai"
  install_tmp=
  printf 'PASS: deployed %s/ai\n' "$bindir" | tee -a "$summary"
fi
printf '\nAll gates passed. Summary: %s\n' "$summary"
