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

# ── hostlink#236: every step fails loudly, the service is verified, and the ──
# ── outcome is written to a status file recovery tooling can read ────────────
sudo() { "$@"; }
INSTALL_STATUS_FILE="$TEMP_DIR/state/install-status"
status_field() { sed -n "s/^$1=//p" "$INSTALL_STATUS_FILE"; }

VERSION="v1.2.3"; STAGE="download"
write_install_status failed 4
assert_eq "$(status_field status)|$(status_field stage)|$(status_field version)|$(status_field exit_code)" \
  "failed|download|v1.2.3|4" "status file records the outcome, the stage and the version"
status_field updated_at | grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]{8}Z$' && pass || fail "status file carries a UTC timestamp: $(status_field updated_at)"
grep -qi "token" "$INSTALL_STATUS_FILE" && fail "status file must never hold the token" || pass

# A failing step stops the install with a non-zero exit, naming the step.
broken_step() { false; }
(run_step move broken_step > "$TEMP_DIR/step.out" 2>&1); code=$?
assert_eq "$code" "$EXIT_INSTALL" "a failing step exits non-zero"
grep -q "step 'move' failed" "$TEMP_DIR/step.out" && pass || fail "the failing step is named: $(cat "$TEMP_DIR/step.out")"
ok_step() { true; }
(run_step env ok_step > /dev/null 2>&1) && pass || fail "a step that succeeds carries on"

# Each install step reports its own failure, not only its last command's.
mv() { return 1; }
(move_bin > /dev/null 2>&1) && fail "move_bin must fail when mv fails" || pass
unset -f mv
mkdir() { return 1; }
(create_directories > /dev/null 2>&1) && fail "create_directories must fail when mkdir fails" || pass
unset -f mkdir
systemctl() { [ "$1" = "start" ] && return 1; return 0; }
cp() { :; }
(install_service > /dev/null 2>&1) && fail "install_service must fail when systemctl start fails" || pass
unset -f systemctl cp

# Verification: up and still up a moment later, else a non-zero exit.
VERIFY_TIMEOUT=3; VERIFY_SETTLE=1
systemctl() { [ "$1" = "is-active" ] && return 0; return 0; }
(verify_service > /dev/null 2>&1) && pass || fail "a running service verifies"
systemctl() { [ "$1" = "is-active" ] && return 3; return 0; }
(verify_service > "$TEMP_DIR/verify.out" 2>&1); code=$?
assert_eq "$code" "$EXIT_SERVICE" "a service that never comes up fails verification"
echo 0 > "$TEMP_DIR/active_calls"
systemctl() {   # up once, then crashed: a crash-looping agent
  if [ "$1" = "is-active" ]; then
    local n; n=$(( $(cat "$TEMP_DIR/active_calls") + 1 )); echo "$n" > "$TEMP_DIR/active_calls"
    [ "$n" -eq 1 ] && return 0 || return 3
  fi
  return 0
}
(verify_service > /dev/null 2>&1); code=$?
assert_eq "$code" "$EXIT_SERVICE" "a service that dies right after starting fails verification"
unset -f systemctl

# The exit trap writes the final outcome.
STAGE="verify"; (finish_install 7 > /dev/null 2>&1)
assert_eq "$(status_field status)|$(status_field stage)|$(status_field exit_code)" "failed|verify|7" "a failed install is recorded with its stage"
STAGE="verify"; (finish_install 0 > /dev/null 2>&1)
assert_eq "$(status_field status)|$(status_field exit_code)" "installed|0" "a verified install is recorded as installed"
# ── --app-port: the agent's port is set before its first start ─────────────
APP_PORT=""
parse_args --token-id tid --app-port 8081
assert_eq "$APP_PORT" "8081" "--app-port is parsed"
HOSTLINK_ENV_FILE="$TEMP_DIR/etc/hostlink.env"; mkdir -p "$TEMP_DIR/etc"
SERVER_URL="https://cp.example"; TOKEN_ID="tid"; TOKEN_KEY="tkey"
chmod() { :; }
(create_env_file > /dev/null 2>&1) && pass || fail "create_env_file succeeds"
grep -qx "SH_APP_PORT=8081" "$HOSTLINK_ENV_FILE" && pass || fail "SH_APP_PORT is written: $(cat "$HOSTLINK_ENV_FILE")"
APP_PORT=""
(create_env_file > /dev/null 2>&1)
grep -q "SH_APP_PORT" "$HOSTLINK_ENV_FILE" && fail "no --app-port, no SH_APP_PORT (the agent's default)" || pass
unset -f chmod

# ── Port taken before start (Coolify: Traefik on 8080, the caller moves the ──
# ── agent and restarts it after the installer): start, but do not verify ────
ss() { [ "$*" = "-Hltn sport = :8080" ] && echo "LISTEN 0 4096 *:8080 *:*"; return 0; }
APP_PORT=""; SKIP_VERIFY=0
check_port_free > "$TEMP_DIR/port.out" 2>&1
assert_eq "$SKIP_VERIFY" "1" "a taken agent port skips verification"
grep -q "8080 is already in use" "$TEMP_DIR/port.out" && pass || fail "the skip is explained: $(cat "$TEMP_DIR/port.out")"
APP_PORT="8081"; SKIP_VERIFY=0
check_port_free > /dev/null 2>&1
assert_eq "$SKIP_VERIFY" "0" "a free --app-port is verified"
ss() { return 0; }
APP_PORT=""; SKIP_VERIFY=0
check_port_free > /dev/null 2>&1
assert_eq "$SKIP_VERIFY" "0" "a free default port is verified"
unset -f ss
if ! command -v ss > /dev/null 2>&1; then   # macOS: no ss at all
  SKIP_VERIFY=0; check_port_free > /dev/null 2>&1
  assert_eq "$SKIP_VERIFY" "1" "no ss: the port cannot be checked, so neither is the service"
fi
STAGE="verify"; SKIP_VERIFY=1; (finish_install 0 > /dev/null 2>&1)
assert_eq "$(status_field status)" "unverified" "an install whose check was skipped says so"
SKIP_VERIFY=0

# ── main, end to end with every step stubbed: the trap records the outcome ──
run_main() {   # $1: is-active answers, one per call (0 up, 3 down)
  echo "$1" > "$TEMP_DIR/answers"
  (
    check_root() { :; }; check_dependencies() { :; }; create_temp_dir() { :; }
    detect_arch() { echo x86_64; }; uninstall_existing() { :; }; download_tar() { :; }
    extract_tar() { :; }; move_bin() { :; }; create_directories() { :; }; create_env_file() { :; }
    install_service() { :; }; ss() { return 0; }
    systemctl() {
      [ "$1" = "is-active" ] || return 0
      local first rest; read -r first rest < "$TEMP_DIR/answers"; echo "${rest:-$first}" > "$TEMP_DIR/answers"
      return "$first"
    }
    main --version v1.2.3
  ) > "$TEMP_DIR/main.out" 2>&1
}
run_main "0 0"; code=$?
assert_eq "$code|$(status_field status)|$(status_field version)" "0|installed|v1.2.3" "a healthy install exits 0, recorded installed"
run_main "0 3"; code=$?
assert_eq "$code|$(status_field status)|$(status_field stage)" "$EXIT_SERVICE|failed|verify" "an agent that crashes after its first check fails the install"

unset -f sudo

echo "install_test.sh: $PASSES passed, $FAILS failed"
[ "$FAILS" -eq 0 ]
