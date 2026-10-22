//go:build integration

package enginemonitor

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hostlink/domain/credential"
	"hostlink/internal/mysqlmetrics"
)

// Against a real MySQL: MYSQL_IT_HOST, MYSQL_IT_PORT, MYSQL_IT_ROOT_PASSWORD
// (a root that may create users). Skipped when unset.
func mysqlAdmin(t *testing.T) credential.Credential {
	t.Helper()
	host := os.Getenv("MYSQL_IT_HOST")
	if host == "" {
		t.Skip("MYSQL_IT_HOST not set")
	}
	port, _ := strconv.Atoi(os.Getenv("MYSQL_IT_PORT"))
	pw := os.Getenv("MYSQL_IT_ROOT_PASSWORD")
	return credential.Credential{Host: host, Port: port, Username: "root", Password: &pw}
}

const itUser = "hostlink_it_monitor"

func rootDB(t *testing.T, admin credential.Credential) *sql.DB {
	db, err := sql.Open("mysql", fmt.Sprintf("root:%s@tcp(%s:%d)/", *admin.Password, admin.Host, admin.Port))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.Exec("DROP USER IF EXISTS '" + itUser + "'@'%'"); db.Close() })
	return db
}

func TestIntegration_MySQLMonitorUserLogsInAndCollects(t *testing.T) {
	admin := mysqlAdmin(t)
	root := rootDB(t, admin)
	password := "MonitorPassword0123456789abcdefghij"

	require.NoError(t, EnsureMySQL(context.Background(), admin, itUser, password))

	var super string
	require.NoError(t, root.QueryRow("SELECT Super_priv FROM mysql.user WHERE user = ?", itUser).Scan(&super))
	assert.Equal(t, "N", super, "never SUPER")

	pw := password
	m, err := mysqlmetrics.New().Collect(credential.Credential{Host: admin.Host, Port: admin.Port, Username: itUser, Password: &pw})
	require.NoError(t, err)
	assert.Greater(t, m.MaxConnections, 0)
}

// A customer who changes or locks the user: the next ensure restores it.
func TestIntegration_MySQLEnsureRestoresATamperedUser(t *testing.T) {
	admin := mysqlAdmin(t)
	root := rootDB(t, admin)
	password := "SecondMonitorPassword0123456789abcdef"
	require.NoError(t, EnsureMySQL(context.Background(), admin, itUser, password))

	_, err := root.Exec("ALTER USER '" + itUser + "'@'%' IDENTIFIED BY 'customer-changed' ACCOUNT LOCK")
	require.NoError(t, err)
	pw := password
	cred := credential.Credential{Host: admin.Host, Port: admin.Port, Username: itUser, Password: &pw}
	_, err = mysqlmetrics.New().Collect(cred)
	require.Error(t, err)

	require.NoError(t, EnsureMySQL(context.Background(), admin, itUser, password))
	_, err = mysqlmetrics.New().Collect(cred)
	assert.NoError(t, err)
}
