#!/usr/bin/env bash
# Mirrors a release's Linux agent to S3 so servers install hostlink without
# GitHub: hostlink_Linux_{x86_64,arm64}.tar.gz, goreleaser's checksums file and
# the installer, under <s3-prefix>/<tag>/. A server then runs
#   install.sh --version <tag> --download-base-url https://<bucket>.s3.amazonaws.com/hostlink/<tag>
# A tarball that does not match goreleaser's checksums is never mirrored, and
# nothing is uploaded unless every asset is present.
#
# Usage: mirror_to_s3.sh <tag> <goreleaser-dist-dir> <s3-prefix>
#   e.g. mirror_to_s3.sh v0.8.45 dist s3://selfhost-hostlink-releases/hostlink
set -euo pipefail

if [ $# -ne 3 ]; then
  echo "Usage: $0 <tag> <goreleaser-dist-dir> <s3-prefix>" >&2
  exit 1
fi

TAG="$1"
DIST="$2"
PREFIX="${3%/}"
VERSION="${TAG#v}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

TARBALLS=(hostlink_Linux_x86_64.tar.gz hostlink_Linux_arm64.tar.gz)
CHECKSUMS="hostlink_${VERSION}_checksums.txt"
INSTALLER="$REPO_ROOT/scripts/linux/install.sh"

for f in "${TARBALLS[@]}" "$CHECKSUMS"; do
  if [ ! -f "$DIST/$f" ]; then
    echo "ERROR: $DIST/$f is missing; nothing mirrored." >&2
    exit 1
  fi
done
if [ ! -f "$INSTALLER" ]; then
  echo "ERROR: $INSTALLER is missing; nothing mirrored." >&2
  exit 1
fi

for f in "${TARBALLS[@]}"; do
  expected=$(awk -v name="$f" '$2 == name { print $1 }' "$DIST/$CHECKSUMS")
  actual=$(sha256sum "$DIST/$f" | cut -d' ' -f1)
  if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
    echo "ERROR: $f does not match $CHECKSUMS (expected '${expected:-none}', got '$actual'); nothing mirrored." >&2
    exit 1
  fi
done

for f in "${TARBALLS[@]}" "$CHECKSUMS"; do
  aws s3 cp --only-show-errors "$DIST/$f" "$PREFIX/$TAG/$f"
done
aws s3 cp --only-show-errors "$INSTALLER" "$PREFIX/$TAG/install.sh"

echo "Mirrored hostlink $TAG to $PREFIX/$TAG/"
