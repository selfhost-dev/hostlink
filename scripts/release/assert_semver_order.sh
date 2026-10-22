#!/usr/bin/env bash
# Asserts that the tags are given in ascending semantic-version order.
#
# Release candidates must sort before the final release:
#   v9.0.2-rc.1 < v9.0.2-rc.2 < v9.0.2
#
# GNU and BSD `sort -V` put "~" before the end of the string, so "-rc." is
# normalized to "~rc." before sorting. This keeps the check portable without
# adding a semver library.
set -euo pipefail

if [ $# -lt 2 ]; then
  echo "Usage: $0 TAG TAG [TAG...]" >&2
  exit 1
fi

normalized=$(printf '%s\n' "$@" | sed 's/-rc\./~rc./')
actual=$(printf '%s\n' "$normalized" | sort -V)
if [ "$actual" != "$normalized" ]; then
  echo "ASSERTION FAILED: tags are not in ascending semver order" >&2
  echo "expected:" >&2
  printf '%s\n' "$@" >&2
  echo "sorted:" >&2
  printf '%s\n' "$actual" >&2
  exit 1
fi

echo "OK: $*"
