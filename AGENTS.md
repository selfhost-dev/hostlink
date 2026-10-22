# AGENTS

- Use git.

- **Branching:** `git switch -c <name>` from `main`, commit with Conventional Commits, then `git push -u origin <name>`.
- **Before push:** `git fetch origin && git rebase origin/main`. Resolve conflicts locally, verify with tests, then push.
- **Conflict resolution:** `git diff --name-only --diff-filter=U` to find conflicts, edit files to remove markers (`<<<<<<<` / `>>>>>>>`), then `git add` and `git rebase --continue`.
- **Undo:** `git restore <file>` discards working tree changes, `git reflog` recovers lost commits.

## Release Strategy

When asked to create release follow these steps:

- Merge the PR if not already merged using squash merge.
- Create a release from `main` branch. Follow semver.
- DO NOT make the new release latest
- Wait for the release pipeline to complete
- Once release is complete, make the release latest

## Agent skills

### Issue tracker

GitHub Issues in `selfhost-dev/hostlink`, via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Domain docs

Single-context: root `GLOSSARY.md` and `docs/adr/`. See `docs/agents/domain.md`.
