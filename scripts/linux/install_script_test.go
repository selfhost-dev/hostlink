package linux_test

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

// install.sh has its own bash tests (install_test.sh): a pinned --version
// never calls GitHub's API, --download-base-url points the download at a
// mirror, per-arch --sha256-* checksums are verified and retried, the
// installer still runs when piped through `bash -s --`, every step fails
// loudly, the service is verified and the outcome lands in the status file
// (hostlink#236). This runs them under `make test`.
func TestInstallScript(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum not available")
	}
	out, err := exec.Command("bash", "install_test.sh").CombinedOutput()
	require.NoError(t, err, string(out))
}
