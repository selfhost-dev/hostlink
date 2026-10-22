# AGENTS

- Use git.

- **Branching:** `git switch -c <name>` from `main`, commit with Conventional Commits, then `git push -u origin <name>`.
- **Before push:** `git fetch origin && git rebase origin/main`. Resolve conflicts locally, verify with tests, then push.
- **Conflict resolution:** `git diff --name-only --diff-filter=U` to find conflicts, edit files to remove markers (`<<<<<<<` / `>>>>>>>`), then `git add` and `git rebase --continue`.
- **Undo:** `git restore <file>` discards working tree changes, `git reflog` recovers lost commits.

## Release Strategy

- Stable releases use `vMAJOR.MINOR.PATCH`; release candidates use
  `vMAJOR.MINOR.PATCH-rc.N`.
- Merge the PR if not already merged using squash merge.
- Create releases from `main`, following semver.
- RCs are GitHub prereleases and must never become `latest`.
- The release workflow promotes a stable release to `latest` automatically
  after its assets and invariants pass.
- Cut the stable tag on the tested RC commit when possible.
- See `docs/releases.md` for the runbook and consumer contract.

## Agent skills

### Issue tracker

GitHub Issues in `selfhost-dev/hostlink`, via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Domain docs

Single-context: root `GLOSSARY.md` and `docs/adr/`. See `docs/agents/domain.md`.
