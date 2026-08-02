---
name: verify
description: "Drive regru CLI changes through the built binary."
---

# Verify regru CLI

Use the real binary surface; do not substitute unit tests for runtime evidence.

1. Build with `go build -o /tmp/regru-verify ./cmd/regru`.
2. Use a temporary `HOME` containing a minimal `regru/config.toml` and a private credential helper; never use the developer's real profile.
3. Drive help, validation, `--dry-run`, `--no-input`, `--plain`, and `--json` directly through `/tmp/regru-verify`.
4. For provider flows, run a local HTTP fixture and build with Go's `-overlay` option, replacing only `cmd/regru/main.go` so the relevant executor receives the fixture `BaseURL`. This keeps the CLI parsing, credential process, runtime, executor, client, waiting, rendering, and exit-code seams intact without contacting REG.RU.
5. Use `expect` for TTY confirmation flows. Capture the fixture request log to prove mutation count, wait ordering, reconciliation reads, and zero network calls for dry-run/no-input paths.
6. Run the repository checks separately after runtime verification: `make check`.
