# Project Instructions

- The `regru` CLI must follow the `$create-cli` skill when designing, implementing, reviewing, or documenting its command-line interface.
- Treat `.agents/skills/create-cli/SKILL.md` and its references as the canonical guidance for commands, arguments, flags, help, output, errors, exit codes, prompts, safety, configuration, signals, and scriptability.

- User-visible changes add one uniquely named `.changes/*.md` fragment with `## Changes` and English one-line bullets. Internal/test-only changes need none. Never edit shared pending changelog entries on feature branches.
- Release preparation uses `make release-notes VERSION=X.Y.Z`; commit generated CHANGELOG.md, release receipts and consumed fragments before creating the native vX.Y.Z tag. See `.changes/README.md`.
