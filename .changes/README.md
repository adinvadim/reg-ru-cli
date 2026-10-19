# Release notes

Add one uniquely named Markdown file per user-visible change, for example
`.changes/20261019-support-timeout.md`:

```md
## Changes

- Explain the change in English from the user's perspective.
```

Keep each bullet on one line. Pure test/internal changes need no fragment.
Feature branches leave `CHANGELOG.md` alone.

Commit fragments first. Run `make release-notes VERSION=X.Y.Z` from the repo
root to assemble committed fragments and any legacy Unreleased entries into a
dated changelog section. Review and commit the generated section, the
`.changes/releases/X.Y.Z.json` receipt and fragment deletions together, then
create the native `vX.Y.Z` tag. This command never tags or publishes.

Preparation pins HEAD. Uncommitted/newer notes do not enter that release.
Cleanup removes only files whose content matches the included snapshot;
modified files remain pending. Receipts prevent duplication after interrupted
cleanup. Repeating a prepared version only retries its exact cleanup. Keep
receipts in Git; never edit them manually. If interrupted between the receipt
and changelog writes, retry restores the exact output when the original changelog
is intact; inconsistent output blocks cleanup. Cleanup retains private recovery
copies under `.changes/.recovery/`, including changes written through
a file handle opened before cleanup. These copies never enter Git. Coordinate
release preparation through one release operator per checkout.
The existing tag workflow reads only the version section committed at the tag,
so publication retries keep the same notes.

`make check-release-notes` validates working fragments without consuming them.
