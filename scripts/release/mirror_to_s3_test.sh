#!/usr/bin/env bash
# Tests for mirror_to_s3.sh with `aws` stubbed on PATH. Run by
# mirror_to_s3_test.go under `make test`, or directly.
set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$HERE/mirror_to_s3.sh"
FAILS=0; PASSES=0
pass() { PASSES=$((PASSES + 1)); }
fail() { FAILS=$((FAILS + 1)); echo "FAIL: $*" >&2; }

WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/bin"
cat > "$WORK/bin/aws" <<STUB
#!/usr/bin/env bash
echo "\$*" >> "$WORK/aws.log"
STUB
chmod +x "$WORK/bin/aws"

make_dist() {
  rm -rf "$WORK/dist"; mkdir -p "$WORK/dist"
  echo x86 | gzip > "$WORK/dist/hostlink_Linux_x86_64.tar.gz"
  echo arm | gzip > "$WORK/dist/hostlink_Linux_arm64.tar.gz"
  (cd "$WORK/dist" && sha256sum hostlink_Linux_x86_64.tar.gz hostlink_Linux_arm64.tar.gz > hostlink_1.2.3_checksums.txt)
  : > "$WORK/aws.log"
}

# ── Happy path: every asset lands under <prefix>/<tag>/ ───────────────────────
make_dist
PATH="$WORK/bin:$PATH" bash "$SCRIPT" v1.2.3 "$WORK/dist" s3://bucket/hostlink > "$WORK/out" 2>&1; code=$?
[ "$code" = 0 ] && pass || fail "happy path exit $code: $(cat "$WORK/out")"
for f in hostlink_Linux_x86_64.tar.gz hostlink_Linux_arm64.tar.gz hostlink_1.2.3_checksums.txt install.sh; do
  grep -q "s3 cp .*/$f s3://bucket/hostlink/v1.2.3/$f" "$WORK/aws.log" && pass || fail "$f not uploaded: $(cat "$WORK/aws.log")"
done

# ── A tarball that does not match goreleaser's checksums is never mirrored ────
make_dist
echo tampered | gzip > "$WORK/dist/hostlink_Linux_arm64.tar.gz"
PATH="$WORK/bin:$PATH" bash "$SCRIPT" v1.2.3 "$WORK/dist" s3://bucket/hostlink > "$WORK/out" 2>&1; code=$?
[ "$code" != 0 ] && pass || fail "checksum mismatch must fail"
[ ! -s "$WORK/aws.log" ] && pass || fail "nothing may be uploaded on a mismatch: $(cat "$WORK/aws.log")"

# ── A missing asset fails before any upload ───────────────────────────────────
make_dist
rm "$WORK/dist/hostlink_Linux_x86_64.tar.gz"
PATH="$WORK/bin:$PATH" bash "$SCRIPT" v1.2.3 "$WORK/dist" s3://bucket/hostlink > "$WORK/out" 2>&1; code=$?
[ "$code" != 0 ] && pass || fail "missing asset must fail"
[ ! -s "$WORK/aws.log" ] && pass || fail "nothing may be uploaded when an asset is missing"

# ── Usage ─────────────────────────────────────────────────────────────────────
bash "$SCRIPT" v1.2.3 > /dev/null 2>&1 && fail "missing arguments must fail" || pass

echo "mirror_to_s3_test.sh: $PASSES passed, $FAILS failed"
[ "$FAILS" -eq 0 ]
