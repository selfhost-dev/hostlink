#!/usr/bin/env bash
# Tests for install.sh. Sources the installer (which must not run when
# sourced) and exercises its functions with curl/sleep stubbed. Run by
# install_script_test.go under `make test`, or directly: bash install_test.sh
set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALLER="$HERE/install.sh"
FAILS=0
PASSES=0

pass() { PASSES=$((PASSES + 1)); }
fail() { FAILS=$((FAILS + 1)); echo "FAIL: $*" >&2; }
assert_eq() { [ "$1" = "$2" ] && pass || fail "$3: expected [$2], got [$1]"; }

# ── Guard: sourcing never runs the installer; executing and piping do ──────────
out=$(bash -c "source '$INSTALLER'; echo sourced-ok" 2>&1)
assert_eq "$(echo "$out" | tail -1)" "sourced-ok" "sourcing install.sh must not run it (no root check, no install)"

out=$(bash "$INSTALLER" --bogus 2>&1); code=$?
assert_eq "$code" "1" "executing runs main (unknown option rejected)"
echo "$out" | grep -q "Unknown option: --bogus" && pass || fail "executing: unknown option message missing: $out"

out=$(bash -s -- --bogus < "$INSTALLER" 2>&1); code=$?
assert_eq "$code" "1" "piped 'bash -s --' still runs main (every existing invocation)"
echo "$out" | grep -q "Unknown option: --bogus" && pass || fail "piped: unknown option message missing: $out"

# Everything below runs against the sourced functions.
# shellcheck source=install.sh
source "$INSTALLER"
sleep() { :; }
TEMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TEMP_DIR"' EXIT

# ── Arguments ─────────────────────────────────────────────────────────────────
parse_args --token-id tid --token-key tkey --server-url https://cp.example \
  --version v1.2.3 --download-base-url https://mirror.example/hostlink/v1.2.3 \
  --sha256-x86_64 aaa --sha256-arm64 bbb
assert_eq "$TOKEN_ID|$TOKEN_KEY|$SERVER_URL" "tid|tkey|https://cp.example" "token and server args"
assert_eq "$VERSION|$DOWNLOAD_BASE_URL" "v1.2.3|https://mirror.example/hostlink/v1.2.3" "version and base url args"
assert_eq "$SHA256_X86_64|$SHA256_ARM64" "aaa|bbb" "per-arch sha256 args"

# ── Version: a pinned version never asks GitHub's API ─────────────────────────
curl() { echo "CURL $*" >> "$TEMP_DIR/curl.log"; return 7; }
VERSION="v9.9.9"
assert_eq "$(resolve_version)" "v9.9.9" "pinned version is used as given"
[ ! -s "$TEMP_DIR/curl.log" ] && pass || fail "pinned version must not call curl: $(cat "$TEMP_DIR/curl.log")"
unset -f curl

# ── Download URL: GitHub by default, the mirror when given ────────────────────
VERSION="v1.2.3"; ARCH="arm64"; DOWNLOAD_BASE_URL=""
assert_eq "$(download_url)" "https://github.com/selfhost-dev/hostlink/releases/download/v1.2.3/hostlink_Linux_arm64.tar.gz" "default download url"
DOWNLOAD_BASE_URL="https://mirror.example/hostlink/v1.2.3/"
assert_eq "$(download_url)" "https://mirror.example/hostlink/v1.2.3/hostlink_Linux_arm64.tar.gz" "mirror download url (trailing slash tolerated)"

# ── Checksum: per-arch expected value, verified when given ────────────────────
good="$TEMP_DIR/good.tar.gz"; echo "hostlink" | gzip > "$good"
good_sha=$(sha256sum "$good" | cut -d' ' -f1)
SHA256_X86_64="xxx"; SHA256_ARM64="$good_sha"; ARCH="arm64"
assert_eq "$(expected_sha256)" "$good_sha" "expected sha256 follows ARCH"
verify_sha256 "$good" && pass || fail "matching sha256 must verify"
SHA256_ARM64="0000"; verify_sha256 "$good" && fail "mismatched sha256 must fail" || pass
SHA256_ARM64=""; verify_sha256 "$good" && pass || fail "no expected sha256 means no check"

# ── download_tar: a checksum mismatch is retried, then the good file accepted ─
check_dependencies   # picks the gzip validator download_tar uses
bad="$TEMP_DIR/bad.tar.gz"; echo "tampered" | gzip > "$bad"
SHA256_ARM64="$good_sha"; HOSTLINK_TAR="hostlink_v1.2.3.tar.gz"; DOWNLOAD_BASE_URL="https://mirror.example/hostlink/v1.2.3"
echo 0 > "$TEMP_DIR/dl_calls"
curl() {
  local out="" n; n=$(( $(cat "$TEMP_DIR/dl_calls") + 1 )); echo "$n" > "$TEMP_DIR/dl_calls"
  echo "${@: -1}" >> "$TEMP_DIR/dl_urls"
  while [ $# -gt 0 ]; do [ "$1" = "-o" ] && out="$2"; shift; done
  if [ "$n" -le 2 ]; then cp "$bad" "$out"; else cp "$good" "$out"; fi
  printf 200
}
(download_tar > "$TEMP_DIR/dl.out" 2>&1); code=$?
assert_eq "$code" "0" "download_tar succeeds once the checksum matches"
assert_eq "$(cat "$TEMP_DIR/dl_calls")" "3" "two checksum mismatches were retried"
assert_eq "$(sort -u "$TEMP_DIR/dl_urls")" "https://mirror.example/hostlink/v1.2.3/hostlink_Linux_arm64.tar.gz" "download_tar fetches from the mirror"
grep -q "from https://mirror.example/hostlink/v1.2.3/hostlink_Linux_arm64.tar.gz" "$TEMP_DIR/dl.out" && pass || fail "download_tar must log its source url"
unset -f curl

# ── create_env_file: update managed keys, preserve operator keys ─────────────
# A reinstall must not silently drop rollout flags or other operator settings.
sudo() { "$@"; }   # create_env_file calls sudo chmod; tests may not run as root
ENV_FILE="$TEMP_DIR/hostlink.env"
cat > "$ENV_FILE" <<'EOF'
SH_CONTROL_PLANE_URL=https://old.example
HOSTLINK_TOKEN_ID=old-id
HOSTLINK_TOKEN_ID=old-id-duplicate
HOSTLINK_TOKEN_KEY=old-key
HOSTLINK_WS_ENABLED=true
HOSTLINK_WS_RESULTS_ENABLED=true
HOSTLINK_WS_DELIVERY_ENABLED=true
HOSTLINK_SELF_UPDATE_ENABLED=false
# operator comment
EOF
SERVER_URL="https://new.example"
TOKEN_ID="new-id"
TOKEN_KEY="new-key"
create_env_file "$ENV_FILE" >/dev/null
for key in SH_CONTROL_PLANE_URL HOSTLINK_TOKEN_ID HOSTLINK_TOKEN_KEY; do
  assert_eq "$(grep -c "^$key=" "$ENV_FILE")" "1" "$key must appear exactly once"
done
assert_eq "$(grep '^SH_CONTROL_PLANE_URL=' "$ENV_FILE")" "SH_CONTROL_PLANE_URL=https://new.example" "control plane URL updated"
assert_eq "$(grep '^HOSTLINK_TOKEN_ID=' "$ENV_FILE")" "HOSTLINK_TOKEN_ID=new-id" "token id updated"
assert_eq "$(grep '^HOSTLINK_TOKEN_KEY=' "$ENV_FILE")" "HOSTLINK_TOKEN_KEY=new-key" "token key updated"
for key in HOSTLINK_WS_ENABLED HOSTLINK_WS_RESULTS_ENABLED HOSTLINK_WS_DELIVERY_ENABLED HOSTLINK_SELF_UPDATE_ENABLED; do
  assert_eq "$(grep -c "^$key=" "$ENV_FILE")" "1" "$key must be preserved"
done
assert_eq "$(grep '^HOSTLINK_WS_ENABLED=' "$ENV_FILE")" "HOSTLINK_WS_ENABLED=true" "websocket rollout flag value preserved"
assert_eq "$(grep '^HOSTLINK_WS_RESULTS_ENABLED=' "$ENV_FILE")" "HOSTLINK_WS_RESULTS_ENABLED=true" "websocket results flag value preserved"
assert_eq "$(grep '^HOSTLINK_WS_DELIVERY_ENABLED=' "$ENV_FILE")" "HOSTLINK_WS_DELIVERY_ENABLED=true" "websocket delivery flag value preserved"
grep -q '^# operator comment$' "$ENV_FILE" && pass || fail "operator comments must be preserved"
env_mode=$(stat -c %a "$ENV_FILE" 2>/dev/null || stat -f %Lp "$ENV_FILE")
assert_eq "$env_mode" "600" "environment file must be mode 600"

FRESH_ENV="$TEMP_DIR/fresh/hostlink.env"
mkdir -p "$(dirname "$FRESH_ENV")"
create_env_file "$FRESH_ENV" >/dev/null
assert_eq "$(cat "$FRESH_ENV")" "$(printf 'SH_CONTROL_PLANE_URL=https://new.example\nHOSTLINK_TOKEN_ID=new-id\nHOSTLINK_TOKEN_KEY=new-key')" "fresh env file contains managed keys"
unset -f sudo

echo "install_test.sh: $PASSES passed, $FAILS failed"
[ "$FAILS" -eq 0 ]
