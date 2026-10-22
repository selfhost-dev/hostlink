package dockerdiscovery

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"

	"hostlink/internal/dockerutil"
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
	DatabaseTypeRedis      DatabaseType = "redis"
)

type DiscoveredDatabase struct {
	Type          DatabaseType
	ContainerID   string
	ContainerName string
	// Host is "" when no address reaching the container is known (see
	// resolveAddress); a collector reports that, it never dials a guess.
	Host     string
	Port     uint16
	Database string
	Username string
	Password string
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
		dbType: DatabaseTypePostgreSQL,
		// postgres: selfhost's Coolify default (postgres:<major>-alpine); the
		// pgvector and timescale images are what it creates a database with
		// extensions on (PostgresqlStrategy / PostgresExtensions::Catalog).
		images: []string{
			"postgres", "supabase/postgres",
			"pgvector/pgvector", "timescale/timescaledb", "timescale/timescaledb-ha",
		},
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
	{
		// selfhost creates a Coolify Redis on redis:<major> (RedisStrategy).
		// coolify-redis is Coolify's own queue, never a customer database.
		dbType:       DatabaseTypeRedis,
		images:       []string{"redis", "bitnami/redis"},
		excludeNames: []string{"coolify-redis"},
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

// extractRedisEnv: Redis has no user (requirepass); Coolify and bitnami pass
// the password as REDIS_PASSWORD.
func extractRedisEnv(envs []string) (string, string, string) {
	password := ""
	for _, env := range envs {
		if k, v, ok := strings.Cut(env, "="); ok && k == "REDIS_PASSWORD" {
			password = v
		}
	}
	return "", password, ""
}

var envExtractors = map[DatabaseType]envExtractor{
	DatabaseTypePostgreSQL: extractPostgresEnv,
	DatabaseTypeMySQL:      extractMySQLEnv,
	DatabaseTypeMongoDB:    extractMongoEnv,
	DatabaseTypeRedis:      extractRedisEnv,
}

var defaultDBName = map[DatabaseType]string{
	DatabaseTypePostgreSQL: "postgres",
	DatabaseTypeMySQL:      "",
	DatabaseTypeMongoDB:    "",
	DatabaseTypeRedis:      "",
}

var defaultPorts = map[DatabaseType]uint16{
	DatabaseTypePostgreSQL: 5432,
	DatabaseTypeMySQL:      3306,
	DatabaseTypeMongoDB:    27017,
	DatabaseTypeRedis:      6379,
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

// DiscoverDatabases lists the running containers once and keeps those whose
// image repository is a database the rules name, whatever its registry, tag
// or digest. It used to ask Docker per bare name with the ancestor filter,
// which resolves "postgres" to postgres:latest: postgres:17-alpine (selfhost's
// Coolify default), mysql:8.4, mongo:7 ... never matched, and the Docker path
// found nothing (selfhost-dev/hostlink#263). Docker unreachable: no databases.
func (d *dockerDiscoverer) DiscoverDatabases(ctx context.Context) ([]DiscoveredDatabase, error) {
	containers, err := d.client.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return nil, nil
	}

	var result []DiscoveredDatabase
	for _, c := range containers {
		db, ok := d.discover(ctx, c)
		if ok {
			result = append(result, db)
		}
	}
	return result, nil
}

func (d *dockerDiscoverer) discover(ctx context.Context, c container.Summary) (DiscoveredDatabase, bool) {
	ref := c.Image
	var info *container.InspectResponse
	if isImageID(ref) {
		// A tag that moved after a re-pull: Docker lists the image by ID, and
		// the name the container was created from is in its config.
		inspected, err := d.client.ContainerInspect(ctx, c.ID)
		if err != nil || inspected.Config == nil {
			return DiscoveredDatabase{}, false
		}
		info, ref = &inspected, inspected.Config.Image
	}

	rule, ok := ruleFor(imageRepository(ref))
	if !ok || isExcluded(c, rule.excludeNames) {
		return DiscoveredDatabase{}, false
	}
	if info == nil {
		inspected, err := d.client.ContainerInspect(ctx, c.ID)
		if err != nil {
			return DiscoveredDatabase{}, false
		}
		info = &inspected
	}
	return describe(c, *info, rule.dbType), true
}

func ruleFor(repository string) (imageRule, bool) {
	for _, rule := range discoveryRules {
		for _, image := range rule.images {
			if repository == image {
				return rule, true
			}
		}
	}
	return imageRule{}, false
}

// imageRepository is an image reference's repository: no registry host, no
// tag, no digest, no implicit "library/" (docker.io/library/postgres:16 and
// postgres@sha256:... are both "postgres"; supabase/postgres:15.8 is
// "supabase/postgres").
func imageRepository(ref string) string {
	ref, _, _ = strings.Cut(ref, "@")
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	parts := strings.Split(ref, "/")
	if len(parts) > 1 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		parts = parts[1:]
	}
	if len(parts) == 2 && parts[0] == "library" {
		parts = parts[1:]
	}
	return strings.Join(parts, "/")
}

func isImageID(ref string) bool {
	return strings.HasPrefix(ref, "sha256:")
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

func describe(c container.Summary, info container.InspectResponse, dbType DatabaseType) DiscoveredDatabase {
	containerName := dockerutil.ResolveContainerName(c.ID, c.Names, c.Labels)

	host, port := resolveAddress(info, defaultPorts[dbType])

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
		Host:          host,
		Port:          port,
		Database:      database,
		Username:      username,
		Password:      password,
	}
}

// resolveAddress is where the agent, running on the host, reaches the
// database inside the container:
//
//   - a port published to the host: that host port, at the address it is
//     bound to (localhost when bound to all interfaces);
//   - otherwise the container's own address on its Docker network, at the
//     container port: IPv4 first, then the global IPv6 address. Coolify never
//     publishes a database container's port (a "public" database is
//     published by a separate <uuid>-proxy container), so this is every
//     Coolify database. localhost:<container port> reached nothing there, and
//     every Coolify PostgreSQL reported up:false;
//   - host networking: localhost at the container port (the host's own stack);
//   - anything else (no network, another container's namespace, no address
//     yet): "" -- no address is known, and dialling a guess would turn "we
//     cannot tell" into a false "down".
//
// With several networks, "coolify" wins, then the first by name, so the
// address is stable between pushes. The host reaches any bridge network's
// addresses, internal ones included; a Swarm overlay or macvlan address is
// not reachable from the host and times out (reported as unknown).
func resolveAddress(info container.InspectResponse, containerPort uint16) (string, uint16) {
	if info.NetworkSettings != nil {
		for cp, bindings := range info.NetworkSettings.Ports {
			if !strings.HasPrefix(string(cp), fmt.Sprintf("%d/", containerPort)) || len(bindings) == 0 {
				continue
			}
			if p := bindings[0].HostPort; p != "" {
				var parsed uint16
				if _, err := fmt.Sscanf(p, "%d", &parsed); err == nil && parsed > 0 {
					return publishedHost(bindings[0].HostIP), parsed
				}
			}
		}

		if ip := networkIP(info.NetworkSettings.Networks); ip != "" {
			return ip, containerPort
		}
	}

	if info.ContainerJSONBase != nil && info.HostConfig != nil && info.HostConfig.NetworkMode.IsHost() {
		return "localhost", containerPort
	}
	return "", containerPort
}

// publishedHost: a port bound to every interface is reached on localhost; one
// bound to a specific address only there.
func publishedHost(hostIP string) string {
	switch hostIP {
	case "", "0.0.0.0", "::":
		return "localhost"
	default:
		return hostIP
	}
}

const preferredNetwork = "coolify"

func networkIP(networks map[string]*network.EndpointSettings) string {
	names := make([]string, 0, len(networks))
	for name := range networks {
		if name != preferredNetwork {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if _, ok := networks[preferredNetwork]; ok {
		names = append([]string{preferredNetwork}, names...)
	}

	for _, pick := range []func(*network.EndpointSettings) string{
		func(ep *network.EndpointSettings) string { return ep.IPAddress },
		func(ep *network.EndpointSettings) string { return ep.GlobalIPv6Address },
	} {
		for _, name := range names {
			if ep := networks[name]; ep != nil && pick(ep) != "" {
				return pick(ep)
			}
		}
	}
	return ""
}

type noopDiscoverer struct{}

func (n *noopDiscoverer) DiscoverDatabases(_ context.Context) ([]DiscoveredDatabase, error) {
	return nil, nil
}
