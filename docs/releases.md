# Releases and release candidates

This page is the canonical release runbook and consumer contract for hostlink.
The short version:

- **Stable release:** tag `vMAJOR.MINOR.PATCH`, for example `v0.8.56`.
- **Release candidate (RC):** tag `vMAJOR.MINOR.PATCH-rc.N`, for example
  `v0.8.56-rc.1`.
- An RC is a GitHub prerelease. It never becomes `latest`.
- A stable release becomes `latest` only after the release workflow verifies
  its assets and invariants.
- Unpinned installs get the latest promoted stable release. Pinned installs get
  the exact tag they name.

See [GLOSSARY.md](../GLOSSARY.md) for the terms and
[ADR 0001](adr/0001-release-candidates-never-move-latest.md) for why RCs never
move `latest`.

## What each consumer sees

| Consumer | Reads | Result |
|---|---|---|
| Installer without `--version` | GitHub `/releases/latest` | Newest stable release |
| Installer with `--version <tag>` | The exact tag; no GitHub API lookup | That tag, RC or stable |
| GitHub release by tag | `/releases/tags/<tag>` | The RC or stable release |
| GitHub `/releases/latest` | Newest promoted stable release | Never an RC |
| Production S3 mirror | `hostlink/<tag>/` | Stable releases only |

`/releases/latest` follows promotion, not tag creation. During a stable
release's verification window, the previous stable release is still returned.
That window ends when the workflow's promotion step succeeds.

The release workflow publishes exactly the same nine assets for an RC and a
stable release:

```text
hlctl_Darwin_arm64.tar.gz
hlctl_Darwin_x86_64.tar.gz
hlctl_Linux_arm64.tar.gz
hlctl_Linux_x86_64.tar.gz
hlctl_Windows_arm64.zip
hlctl_Windows_x86_64.zip
hostlink_<version>_checksums.txt
hostlink_Linux_arm64.tar.gz
hostlink_Linux_x86_64.tar.gz
```

## Tag and version rules

RCs sort before their stable release:

```text
v0.8.56-rc.1 < v0.8.56-rc.2 < v0.8.56
```

Rules:

1. Never reuse or move a published tag.
2. A failed RC is replaced by the next `rc.N`; it is not edited into a stable
   release.
3. Prefer cutting the stable tag on the exact commit tested as the RC. Only the
   version string should differ.
4. A stable release must not point at an older commit than its validated RC
   unless the difference is deliberate and re-tested.

The release workflow only runs for the two release tag shapes above. Other tags
do not create releases.

## Release candidate runbook

1. Merge the change to `main` and confirm CI is green.

2. Cut the RC from `main`:

   ```bash
   git switch main
   git pull --ff-only
   git tag v0.8.56-rc.1
   git push origin refs/tags/v0.8.56-rc.1
   ```

3. Watch the release workflow. It must report:

   ```text
   Run GoReleaser                  success
   Assert release invariants       success
   Configure AWS credentials       skipped
   Mirror release to S3            skipped
   Promote release to latest       skipped
   ```

4. Verify GitHub's state:

   ```bash
   scripts/release/assert_release_invariants.sh \
     --repo selfhost-dev/hostlink \
     --tag v0.8.56-rc.1 \
     --expect-latest v0.8.55
   ```

   The RC must have `draft=false`, `prerelease=true`, nine assets, and must not
   be `/releases/latest`.

5. Deploy the RC to a test server using the
   [server deployment steps](#deploy-an-rc-to-a-server) below.

6. If testing fails, fix on `main` and cut `v0.8.56-rc.2`. Keep the failed RC as
   an immutable prerelease, or delete it only if nothing pins it.

## Deploy an RC to a server

The installer is the deployment mechanism; there is no separate updater command
for a pinned RC. It stops the existing service, verifies the tarball, replaces
`/usr/bin/hostlink`, updates `/etc/hostlink/hostlink.env`, and starts the
service again.

Run this on the server with root access. Reuse the control-plane URL and token
already configured for that server: the installer replaces those three keys
with the values passed here. Other keys and comments in the file are preserved.

1. Choose the tag and release URL.

   ```bash
   V=v0.8.56-rc.1
   BASE="https://github.com/selfhost-dev/hostlink/releases/download/$V"
   ```

   If this RC was copied to an S3 mirror, set `BASE` to that tag's prefix and
   download `install.sh` from the mirror instead.

2. Fetch the checksums and installer.

   ```bash
   curl -fsSL "$BASE/hostlink_${V#v}_checksums.txt" -o /tmp/hostlink-checksums.txt
   curl -fsSL "https://raw.githubusercontent.com/selfhost-dev/hostlink/$V/scripts/linux/install.sh" \
     -o /tmp/hostlink-install.sh

   X86_SHA=$(awk '$2 == "hostlink_Linux_x86_64.tar.gz" { print $1 }' /tmp/hostlink-checksums.txt)
   ARM_SHA=$(awk '$2 == "hostlink_Linux_arm64.tar.gz" { print $1 }' /tmp/hostlink-checksums.txt)
   ```

3. Install the exact tag with checksum verification.

   ```bash
   sudo bash /tmp/hostlink-install.sh \
     --server-url "https://your-control-plane.example" \
     --token-id "your-token-id" \
     --token-key "your-token-key" \
     --version "$V" \
     --download-base-url "$BASE" \
     --sha256-x86_64 "$X86_SHA" \
     --sha256-arm64 "$ARM_SHA"
   ```

   `--version` bypasses `/releases/latest`. The installer picks the checksum
   for the server's architecture; passing both is safe. Reinstalling replaces
   only `SH_CONTROL_PLANE_URL`, `HOSTLINK_TOKEN_ID`, and `HOSTLINK_TOKEN_KEY`;
   rollout flags such as `HOSTLINK_WS_*` and other operator settings stay in
   place.

4. Verify the binary and service.

   ```bash
   hostlink --version
   systemctl is-active hostlink
   journalctl -u hostlink -n 50 --no-pager
   ```

   Expected binary version: `hostlink version 0.8.56-rc.1`.

To return the server to stable, repeat the same steps with the stable tag and
its checksums. The binary and service are replaced; the agent data directory is
not removed.

## Stable release runbook

Cut the stable release on the tested RC commit:

```bash
git switch main
git pull --ff-only
git tag v0.8.56 v0.8.56-rc.1^{commit}
git push origin refs/tags/v0.8.56
```

Both tags can point at the same commit. The workflow passes the pushed tag as
`GORELEASER_CURRENT_TAG`, so GoReleaser builds and publishes `v0.8.56` instead
of selecting the RC tag with `git describe`.

The workflow then:

1. Builds and publishes the full release with `make_latest=false`.
2. Checks that the release is not a prerelease and carries the nine assets.
3. Mirrors the stable release to the production S3 bucket, when configured.
4. Promotes the release to `latest` after verification.

The RC remains visible as a prerelease. It does not move back to `latest`.

If the final workflow fails before promotion, `latest` stays on the previous
stable release. Fix the failure and re-run; do not move the tag.

## Changelog range

RC tags are ignored when GoReleaser finds the previous tag. Both the RC and the
stable release therefore list changes since the last stable release, not just
the delta after the previous RC.

## S3 mirror

The production mirror is documented in
[s3-release-mirror.md](s3-release-mirror.md). Stable releases are copied under
`hostlink/<tag>/`; RCs are not copied to the production bucket by this
workflow. There is no mutable `latest` key: consumers always name a tag.

A test environment that needs an S3 copy of an RC can mirror it separately with
the same script and tag layout. That environment must pin the exact RC tag; it
must not change what unpinned installs resolve to.

## Rollback

- **Bad RC:** stop using it. Cut the next `rc.N` from a fixed `main`.
- **RC accidentally promoted:** restore the previous stable with
  `gh release edit <previous-stable> --latest`, then mark the RC as a
  prerelease again.
- **Bad stable promotion:** move `latest` back to the previous stable with
  `gh release edit <previous-stable> --latest`.
- **Bad workflow change:** revert the workflow commit. Published tags and assets
  stay immutable.

Never delete a tag that a deployed environment pins unless that environment is
being moved first.
