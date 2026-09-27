# Working on agent-cli

Read the ongoing audit in `docs/audits/2026-09-15/README.md` before changing the
provider integrations. Keep its completed and remaining work accurate.

After changes, run `scripts/build.sh`. This is the complete build gate:
format validation, module verification, vet, Staticcheck, size limits, host
race/debug tests, release/debug builds, binary vulnerability scanning, secret
scanning, and isolated CLI smoke tests.
Skipped tests fail the gate. Logs and the final summary are in `bin/build-logs/`.

## Size limits

The `size-limits` step runs `internal/codecheck`: 1000 lines per code file, 100
lines per function including function literals. Files past 600 lines are
reported as a notice, not a failure, so the seam gets found while splitting is
still cheap.

`.codecheck-baseline` lists the functions that were already oversized when the
check was wired in, each with the size it had then. **The list may only shrink.**
A function that is not listed fails. A listed function that grew past its
recorded size fails, so an entry never becomes a licence to keep adding to it. A
listed function that now fits fails as stale, so the entry is deleted when the
code is finally split. Most entries are cobra command constructors whose `RunE`
closure holds the whole command body.

When you touch a baselined function, prefer splitting it and deleting its entry
over raising the number.

When deployment is requested, run `scripts/build.sh --deploy`; it performs the
same checks before atomically installing into `${BINDIR:-${PREFIX:-$HOME/.local}/bin}`.
Do not use `make install` or copy a binary to bypass a failed gate. Fix the failing
step; do not weaken checks merely to make the build pass.

Exercise destructive provider operations only against temporary fixtures during
validation. The user's actual account, memory, skill, plugin, and recovery data
must not be used as disposable test data.
