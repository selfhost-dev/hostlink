package dockerdiscovery

import (
	"context"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

type dockerClient interface {
	ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error)
	ContainerInspect(ctx context.Context, containerID string) (container.InspectResponse, error)
}

type DatabaseType string

const (
	DatabaseTypePostgreSQL DatabaseType = "postgresql"
	DatabaseTypeMySQL      DatabaseType = "mysql"
	DatabaseTypeMongoDB    DatabaseType = "mongodb"
)

type DiscoveredDatabase struct {
	Type          DatabaseType
	ContainerID   string
	ContainerName string
	Host          string
	Port          uint16
	Database      string
	Username      string
	Password      string
}

type Discoverer interface {
	DiscoverDatabases(ctx context.Context) ([]DiscoveredDatabase, error)
}

type imageRule struct {
	dbType       DatabaseType
	images       []string
	excludeNames []string
}

var discoveryRules = []imageRule{
	{
		dbType:       DatabaseTypePostgreSQL,
		images:       []string{"postgres", "supabase/postgres"},
		excludeNames: []string{"coolify-db"},
	},
	{
		dbType:       DatabaseTypeMySQL,
		images:       []string{"mysql", "mariadb"},
		excludeNames: nil,
	},
	{
		dbType:       DatabaseTypeMongoDB,
		images:       []string{"mongo"},
		excludeNames: nil,
	},
}

type envExtractor func(envs []string) (username, password, database string)

func extractPostgresEnv(envs []string) (string, string, string) {
	username := "postgres"
	database := "postgres"
	password := ""
	for _, env := range envs {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "POSTGRES_USER":
			username = parts[1]
		case "POSTGRES_DB":
			database = parts[1]
		case "POSTGRES_PASSWORD":
			password = parts[1]
		}
	}
	return username, password, database
}

func extractMySQLEnv(envs []string) (string, string, string) {
	username := "root"
	database := ""
	password := ""
	for _, env := range envs {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "MYSQL_USER":
			username = parts[1]
		case "MYSQL_ROOT_PASSWORD":
			if password == "" {
				password = parts[1]
			}
		case "MYSQL_PASSWORD":
			password = parts[1]
		case "MYSQL_DATABASE":
			database = parts[1]
		}
	}
	return username, password, database
}

func extractMongoEnv(envs []string) (string, string, string) {
	username := ""
	database := ""
	password := ""
	for _, env := range envs {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "MONGO_INITDB_ROOT_USERNAME":
			username = parts[1]
		case "MONGO_INITDB_ROOT_PASSWORD":
			password = parts[1]
		case "MONGO_INITDB_DATABASE":
			database = parts[1]
		}
	}
	return username, password, database
}

var envExtractors = map[DatabaseType]envExtractor{
	DatabaseTypePostgreSQL: extractPostgresEnv,
	DatabaseTypeMySQL:      extractMySQLEnv,
	DatabaseTypeMongoDB:    extractMongoEnv,
}

var defaultDBName = map[DatabaseType]string{
	DatabaseTypePostgreSQL: "postgres",
	DatabaseTypeMySQL:      "",
	DatabaseTypeMongoDB:    "",
}

var defaultPorts = map[DatabaseType]uint16{
	DatabaseTypePostgreSQL: 5432,
	DatabaseTypeMySQL:      3306,
	DatabaseTypeMongoDB:    27017,
}

type dockerDiscoverer struct {
	client dockerClient
}

var _ Discoverer = (*dockerDiscoverer)(nil)

func New() Discoverer {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return &noopDiscoverer{}
	}
	return &dockerDiscoverer{client: cli}
}

func NewWithClient(cli dockerClient) Discoverer {
	return &dockerDiscoverer{client: cli}
}

func (d *dockerDiscoverer) DiscoverDatabases(ctx context.Context) ([]DiscoveredDatabase, error) {
	var result []DiscoveredDatabase
	for _, rule := range discoveryRules {
		dbs, err := d.discoverByRule(ctx, rule)
		if err != nil {
			continue
		}
		result = append(result, dbs...)
	}
	return result, nil
}

func (d *dockerDiscoverer) discoverByRule(ctx context.Context, rule imageRule) ([]DiscoveredDatabase, error) {
	seen := map[string]bool{}
	var result []DiscoveredDatabase
	for _, image := range rule.images {
		containers, err := d.client.ContainerList(ctx, container.ListOptions{
			Filters: filters.NewArgs(filters.Arg("ancestor", image)),
		})
		if err != nil {
			continue
		}
		for _, c := range containers {
			if seen[c.ID] || isExcluded(c, rule.excludeNames) {
				continue
			}
			seen[c.ID] = true
			db, err := inspectContainer(ctx, d.client, c, rule.dbType)
			if err != nil {
				continue
			}
			result = append(result, db)
		}
	}
	return result, nil
}

func isExcluded(c container.Summary, excludes []string) bool {
	if len(excludes) == 0 {
		return false
	}
	for _, n := range c.Names {
		name := strings.TrimPrefix(n, "/")
		for _, ex := range excludes {
			if name == ex || strings.HasPrefix(name, ex+"-") {
				return true
			}
		}
	}
	return false
}

func inspectContainer(ctx context.Context, cli dockerClient, c container.Summary, dbType DatabaseType) (DiscoveredDatabase, error) {
	info, err := cli.ContainerInspect(ctx, c.ID)
	if err != nil {
		return DiscoveredDatabase{}, fmt.Errorf("inspect %s: %w", c.ID[:12], err)
	}

	containerName := strings.TrimPrefix(c.Names[0], "/")

	port := defaultPorts[dbType]
	if info.NetworkSettings != nil {
		for containerPort, bindings := range info.NetworkSettings.Ports {
			if strings.HasPrefix(string(containerPort), fmt.Sprintf("%d/", port)) && len(bindings) > 0 {
				if p := bindings[0].HostPort; p != "" {
					var parsed uint16
					if _, err := fmt.Sscanf(p, "%d", &parsed); err == nil && parsed > 0 {
						port = parsed
						break
					}
				}
			}
		}
	}

	username, password, database := "", "", ""
	if info.Config != nil {
		extractor := envExtractors[dbType]
		if extractor != nil {
			username, password, database = extractor(info.Config.Env)
		}
	}

	if database == "" {
		database = defaultDBName[dbType]
	}

	return DiscoveredDatabase{
		Type:          dbType,
		ContainerID:   c.ID,
		ContainerName: containerName,
		Host:          "localhost",
		Port:          port,
		Database:      database,
		Username:      username,
		Password:      password,
	}, nil
}

type noopDiscoverer struct{}

func (n *noopDiscoverer) DiscoverDatabases(_ context.Context) ([]DiscoveredDatabase, error) {
	return nil, nil
}
