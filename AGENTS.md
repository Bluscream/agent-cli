# Working on agent-cli

Read the ongoing audit in `docs/audits/2026-09-15/README.md` before changing the
provider integrations. Keep its completed and remaining work accurate.

After changes, run `scripts/build.sh`. This is the complete build gate:
format validation, module verification, vet, Staticcheck, host race/debug tests,
release/debug builds, binary vulnerability scanning, and isolated CLI smoke tests.
Skipped tests fail the gate. Logs and the final summary are in `bin/build-logs/`.

When deployment is requested, run `scripts/build.sh --deploy`; it performs the
same checks before atomically installing into `${BINDIR:-${PREFIX:-$HOME/.local}/bin}`.
Do not use `make install` or copy a binary to bypass a failed gate. Fix the failing
step; do not weaken checks merely to make the build pass.

Exercise destructive provider operations only against temporary fixtures during
validation. The user's actual account, memory, skill, plugin, and recovery data
must not be used as disposable test data.
