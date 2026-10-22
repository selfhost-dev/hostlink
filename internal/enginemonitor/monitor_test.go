package enginemonitor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestMySQLStatements_MinimalAccountAndQuotedPassword(t *testing.T) {
	stmts, err := MySQLStatements("selfhost_monitor", `p'w\x`)
	require.NoError(t, err)
	all := strings.Join(stmts, "\n")

	assert.Contains(t, all, "SET SESSION lock_wait_timeout = 10", "a customer's open DDL cannot stall the agent")
	assert.Contains(t, all, `CREATE USER IF NOT EXISTS 'selfhost_monitor'@'%' IDENTIFIED BY 'p\'w\\x'`)
	assert.Contains(t, all, "WITH MAX_USER_CONNECTIONS 3 ACCOUNT UNLOCK", "a locked or altered account is restored")
	assert.Contains(t, all, "GRANT PROCESS, REPLICATION CLIENT ON *.*")
	assert.Contains(t, all, "GRANT SELECT ON performance_schema.*")
	assert.NotContains(t, all, "ALL PRIVILEGES")
	assert.NotContains(t, all, "SUPER")
}

func TestMySQLStatements_RefuseAnUnsafeName(t *testing.T) {
	_, err := MySQLStatements("x'@'%' ; DROP USER root", "pw")
	assert.Error(t, err)
}

func TestMongoRoles_ClusterMonitorOnly(t *testing.T) {
	assert.Equal(t, bson.A{bson.D{{Key: "role", Value: "clusterMonitor"}, {Key: "db", Value: "admin"}}}, MongoRoles)
}
