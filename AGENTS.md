# AGENTS

- Use jj instead of git. Check with `ls .jj/` — if no `.jj` directory exists, fall back to git.

- **Branching:** `jj describe -m "type(scope): message"` on `@`, then `jj bookmark create <name>` and `jj git push -b <name>`.
- **Before push:** `jj git fetch && jj rebase -b @ -o main`. Resolve conflicts locally, verify with tests, then push.
- **Conflict resolution:** `jj resolve --list` to find conflicts, edit files to remove markers (`<<<<<<<` / `>>>>>>>`), then squash resolution with `jj squash` (no `--interactive` flag). No detached HEAD or rebase-in-progress state to manage.
- **Undo:** `jj op undo` reverts any operation. Safe to experiment.

## Release Strategy

When asked to create release follow these steps:

- Merge the PR if not already merged using squash merge.
- Create a release from `main` branch. Follow semver.
- DO NOT make the new release latest
- Wait for the release pipeline to complete
- Once release is complete, make the release latest
