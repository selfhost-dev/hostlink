package mysqlmetrics

import (
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hostlink/domain/credential"
)

// A TLS-enabled MySQL member runs require_secure_transport=ON, which refuses a
// plaintext TCP login, loopback included. go-sql-driver is plaintext unless the
// DSN says otherwise, so the collector's ping failed on every enforcing member,
// the agent reported the database down, and the control plane failed it over
// (selfhost-dev/selfhost#3393). The DSN must ask for TLS whenever the server
// offers it, and stay usable on a member that serves none.
func TestDSN_NegotiatesTLSWhenTheServerOffersIt(t *testing.T) {
	password := "secret"
	cfg, err := mysql.ParseDSN(buildDSN(credential.Credential{
		Username: "selfhostadmin", Password: &password, Host: "localhost", Port: 3306,
	}))
	require.NoError(t, err)

	assert.Equal(t, "preferred", cfg.TLSConfig, "TLS when offered")
	assert.NotNil(t, cfg.TLS, "a TLS config the driver will use")
	assert.True(t, cfg.AllowFallbackToPlaintext, "and plaintext on a member that serves no TLS")
	assert.Equal(t, "tcp", cfg.Net)
	assert.Equal(t, "localhost:3306", cfg.Addr)
	assert.Equal(t, "selfhostadmin", cfg.User)
	assert.Equal(t, "secret", cfg.Passwd)
}

func TestDSN_KeepsTheDatabaseAndTimeouts(t *testing.T) {
	cfg, err := mysql.ParseDSN(buildDSN(credential.Credential{
		Username: "u", Host: "localhost", Port: 3307, Database: "app",
	}))
	require.NoError(t, err)

	assert.Equal(t, "app", cfg.DBName)
	assert.Equal(t, "localhost:3307", cfg.Addr)
	assert.Equal(t, "10s", cfg.Timeout.String())
	assert.Equal(t, "10s", cfg.ReadTimeout.String())
	assert.Equal(t, "preferred", cfg.TLSConfig)
}

func TestDSN_HonorsCredentialSSLMode(t *testing.T) {
	password := "secret"
	cfg, err := mysql.ParseDSN(buildDSN(credential.Credential{
		Username: "selfhostadmin", Password: &password, Host: "localhost", Port: 3306, SSLMode: "skip-verify",
	}))
	require.NoError(t, err)
	assert.Equal(t, "skip-verify", cfg.TLSConfig)
}
