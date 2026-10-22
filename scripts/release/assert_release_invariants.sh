#!/usr/bin/env bash
# Asserts the GitHub release invariants for one tag.
#
# Run by .github/workflows/release.yml for every release, and by the RC
# verification runbook. Requires gh and jq.
#
# Invariants:
#   - the release is published (not a draft);
#   - -rc.N tags are GitHub prereleases, all other tags are full releases;
#   - /releases/latest is a full release and, for an RC, is not this tag;
#   - the release carries exactly the expected 9 assets.
set -euo pipefail

REPO=""
TAG=""
EXPECT_LATEST=""

usage() {
  echo "Usage: $0 --repo OWNER/REPO --tag TAG [--expect-latest TAG]" >&2
}

while [ $# -gt 0 ]; do
  case "$1" in
    --repo) REPO="$2"; shift 2 ;;
    --tag) TAG="$2"; shift 2 ;;
    --expect-latest) EXPECT_LATEST="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage; exit 1 ;;
  esac
done

if [ -z "$REPO" ] || [ -z "$TAG" ]; then
  usage
  exit 1
fi

fail() {
  echo "ASSERTION FAILED: $*" >&2
  exit 1
}

is_rc=false
case "$TAG" in
  *-rc.*) is_rc=true ;;
esac

release=$(gh api "repos/$REPO/releases/tags/$TAG")
draft=$(jq -r '.draft' <<<"$release")
prerelease=$(jq -r '.prerelease' <<<"$release")

[ "$draft" = "false" ] || fail "$TAG is still a draft"
if [ "$is_rc" = true ]; then
  [ "$prerelease" = "true" ] || fail "$TAG must be a GitHub prerelease"
else
  [ "$prerelease" = "false" ] || fail "$TAG must not be a GitHub prerelease"
fi

version=${TAG#v}
expected_assets=$(
  printf '%s\n' \
    'hlctl_Darwin_arm64.tar.gz' \
    'hlctl_Darwin_x86_64.tar.gz' \
    'hlctl_Linux_arm64.tar.gz' \
    'hlctl_Linux_x86_64.tar.gz' \
    'hlctl_Windows_arm64.zip' \
    'hlctl_Windows_x86_64.zip' \
    "hostlink_${version}_checksums.txt" \
    'hostlink_Linux_arm64.tar.gz' \
    'hostlink_Linux_x86_64.tar.gz' | sort
)
actual_assets=$(jq -r '[.assets[].name] | sort | .[]' <<<"$release")
count=$(jq -r '.assets | length' <<<"$release")
if [ "$count" -ne 9 ]; then
  echo "expected 9 assets, found $count:" >&2
  echo "$actual_assets" >&2
  exit 1
fi
if [ "$actual_assets" != "$expected_assets" ]; then
  echo "asset set mismatch; expected:" >&2
  echo "$expected_assets" >&2
  echo "actual:" >&2
  echo "$actual_assets" >&2
  exit 1
fi

latest=$(gh api "repos/$REPO/releases/latest")
latest_tag=$(jq -r '.tag_name' <<<"$latest")
latest_draft=$(jq -r '.draft' <<<"$latest")
latest_prerelease=$(jq -r '.prerelease' <<<"$latest")

[ "$latest_draft" = "false" ] || fail "/releases/latest ($latest_tag) is a draft"
[ "$latest_prerelease" = "false" ] || fail "/releases/latest ($latest_tag) is a prerelease"
if [ "$is_rc" = true ]; then
  [ "$latest_tag" != "$TAG" ] || fail "RC $TAG is /releases/latest"
fi
if [ -n "$EXPECT_LATEST" ]; then
  [ "$latest_tag" = "$EXPECT_LATEST" ] || fail "/releases/latest is $latest_tag, expected $EXPECT_LATEST"
fi

echo "OK: $TAG draft=$draft prerelease=$prerelease assets=$count latest=$latest_tag"
