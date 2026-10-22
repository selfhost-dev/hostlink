# Release candidates never move latest

Release candidates are published as GitHub prereleases, and the release
workflow never promotes them. Stable releases are published with
`make_latest=false` and promoted to `latest` only after the workflow verifies
the nine assets and the release invariants.

This protects unpinned installs, which read `/releases/latest`, from receiving
an untested RC. It also keeps the decision visible in the workflow instead of
relying on GitHub's default "newest release becomes latest" behavior.

## Considered Options

- **Draft-first, then publish** (PR #165): reverted in PR #166 because it still
  required an explicit choice about which release is latest.
- **Let GitHub promote the newest release automatically:** rejected because an
  RC, or a stable release whose assets are incomplete, could become `latest`.
- **Promote the RC during testing:** rejected because it would move unpinned
  installs onto an RC.

## Consequences

- RC tags are immutable; a failed RC is replaced by the next `rc.N`.
- The stable tag should point at the exact commit tested as the RC.
- RC tags are excluded when GoReleaser calculates the changelog range.
- The production S3 mirror receives stable releases only; test environments
  that need an RC mirror it separately under the same tag prefix.
