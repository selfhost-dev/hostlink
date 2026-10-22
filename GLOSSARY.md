# Hostlink

Hostlink is the agent and control-plane client for self-hosted machines.

## Language

**Stable release**:
A `vMAJOR.MINOR.PATCH` tag intended for general use.
_Avoid_: final release, production release

**Release candidate (RC)**:
A `vMAJOR.MINOR.PATCH-rc.N` prerelease used to test a future stable release.
_Avoid_: beta, preview

**`latest`**:
GitHub's pointer to the most recently promoted stable release. It never points
at a release candidate.
_Avoid_: newest release, current release

**Promotion**:
Setting a stable release as `latest` after its release workflow verifies the
assets and invariants.
_Avoid_: publishing, releasing

**Pinned install**:
Installing an exact release tag with the installer's `--version` flag, without
reading `/releases/latest`.
_Avoid_: versioned install
