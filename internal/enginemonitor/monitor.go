// Package enginemonitor creates and maintains the agent's own monitoring user
// in a Coolify MySQL or MongoDB container, so the agent never monitors as the
// customer, whose password the customer can change (ALTER USER /
// db.changeUserPassword) without the platform seeing it.
//
// Unlike PostgreSQL (pgmonitorrole, the local socket the official image
// trusts), neither image lets a local process in without a password, so the
// user is created over the network as the database's root, with the admin
// password the platform stored at creation. That works for as long as the
// customer has not changed the root password; after that the agent falls back
// to the platform's record of the customer's login.
package enginemonitor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"hostlink/domain/credential"
)

var safeUser = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,31}$`)

// MySQLStatements: created when missing, then forced to a minimal account
// every time (a customer's changes to it are undone). PROCESS and
// REPLICATION CLIENT read SHOW GLOBAL STATUS / processlist / replica status;
// performance_schema is read-only. lock_wait_timeout keeps a customer's open
// DDL from stalling the agent.
func MySQLStatements(user, password string) ([]string, error) {
	if !safeUser.MatchString(user) {
		return nil, fmt.Errorf("unsafe user name %q", user)
	}
	pw := mysqlQuote(password)
	account := fmt.Sprintf("'%s'@'%%'", user)
	return []string{
		"SET SESSION lock_wait_timeout = 10",
		fmt.Sprintf("CREATE USER IF NOT EXISTS %s IDENTIFIED BY %s", account, pw),
		fmt.Sprintf("ALTER USER %s IDENTIFIED BY %s WITH MAX_USER_CONNECTIONS 3 ACCOUNT UNLOCK", account, pw),
		fmt.Sprintf("GRANT PROCESS, REPLICATION CLIENT ON *.* TO %s", account),
		fmt.Sprintf("GRANT SELECT ON performance_schema.* TO %s", account),
	}, nil
}

func mysqlQuote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// EnsureMySQL runs MySQLStatements as admin (the database's root), over the
// container's address.
func EnsureMySQL(ctx context.Context, admin credential.Credential, user, password string) error {
	stmts, err := MySQLStatements(user, password)
	if err != nil {
		return err
	}
	adminPassword := ""
	if admin.Password != nil {
		adminPassword = *admin.Password
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/?timeout=10s&readTimeout=15s&tls=preferred",
		admin.Username, adminPassword, admin.Host, admin.Port)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("admin login: %w", err)
	}
	defer conn.Close()
	for _, stmt := range stmts {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", strings.SplitN(stmt, " IDENTIFIED", 2)[0], err)
		}
	}
	return nil
}

// MongoRoles: read-only cluster monitoring (serverStatus, replSetGetStatus,
// ...), nothing on customer data.
var MongoRoles = bson.A{bson.D{{Key: "role", Value: "clusterMonitor"}, {Key: "db", Value: "admin"}}}

// EnsureMongo creates the user in admin, or resets its password and roles.
func EnsureMongo(ctx context.Context, admin credential.Credential, user, password string) error {
	if !safeUser.MatchString(user) {
		return fmt.Errorf("unsafe user name %q", user)
	}
	adminPassword := ""
	if admin.Password != nil {
		adminPassword = *admin.Password
	}
	uri := (&url.URL{
		Scheme:   "mongodb",
		User:     url.UserPassword(admin.Username, adminPassword),
		Host:     fmt.Sprintf("%s:%d", admin.Host, admin.Port),
		Path:     "/",
		RawQuery: "authSource=admin&connectTimeoutMS=10000&serverSelectionTimeoutMS=10000&directConnection=true",
	}).String()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background()) //nolint:errcheck

	adminDB := client.Database("admin")
	var info struct {
		Users []bson.M `bson:"users"`
	}
	if err := adminDB.RunCommand(ctx, bson.D{{Key: "usersInfo", Value: user}}).Decode(&info); err != nil {
		return fmt.Errorf("admin login: %w", err)
	}
	command := "createUser"
	if len(info.Users) > 0 {
		command = "updateUser"
	}
	res := adminDB.RunCommand(ctx, bson.D{
		{Key: command, Value: user},
		{Key: "pwd", Value: password},
		{Key: "roles", Value: MongoRoles},
	})
	if err := res.Err(); err != nil {
		return fmt.Errorf("%s: %w", command, err)
	}
	return nil
}

// ErrNoAdmin: the platform supplied no admin login for this container.
var ErrNoAdmin = errors.New("no admin login to create the monitoring user with")

// DefaultTimeout bounds one ensure.
const DefaultTimeout = 30 * time.Second
