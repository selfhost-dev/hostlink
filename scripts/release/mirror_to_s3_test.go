package release_test

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

// mirror_to_s3.sh copies a release's Linux agent tarballs, checksums and
// install.sh to the S3 mirror, refusing a tarball that does not match
// goreleaser's checksums. Its bash tests stub `aws`; this runs them under
// `make test`.
func TestMirrorToS3Script(t *testing.T) {
	for _, bin := range []string{"bash", "sha256sum", "gzip"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(bin + " not available")
		}
	}
	out, err := exec.Command("bash", "mirror_to_s3_test.sh").CombinedOutput()
	require.NoError(t, err, string(out))
}
