package dockerdiscovery

import (
	"context"
	"errors"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// selfhost-dev/hostlink#263: Docker's ancestor filter resolves a bare name to
// :latest, so ancestor=postgres never matched postgres:17-alpine (selfhost's
// Coolify default) and the Docker path found nothing. Discovery now lists the
// running containers once and matches each one's image repository, whatever
// its registry, tag or digest.
func TestDiscoverDatabases_FindsTaggedImagesAsRealDockerListsThem(t *testing.T) {
	cli := new(mockDockerClient)
	d := NewWithClient(cli)
	cli.On("ContainerList", mock.Anything, mock.Anything).Return([]container.Summary{
		{ID: "pg-alpine", Image: "postgres:17-alpine", Names: []string{"/dbuuid1"}},
		{ID: "pg-hub", Image: "docker.io/library/postgres:16", Names: []string{"/dbuuid2"}},
		{ID: "pg-digest", Image: "postgres@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Names: []string{"/dbuuid3"}},
		{ID: "supa", Image: "supabase/postgres:15.8.1.060", Names: []string{"/supabase-db-x"}},
		{ID: "my", Image: "mysql:8.4", Names: []string{"/mysql-x"}},
		{ID: "maria", Image: "mariadb:11.4", Names: []string{"/maria-x"}},
		{ID: "mongo", Image: "mongo:7.0", Names: []string{"/mongo-x"}},
		{ID: "postgrest", Image: "postgrest/postgrest:v12.2.3", Names: []string{"/rest-x"}},
		{ID: "other-pg", Image: "ghcr.io/acme/postgres:1", Names: []string{"/acme-x"}},
		{ID: "coolify-db", Image: "postgres:15-alpine", Names: []string{"/coolify-db"}},
	}, nil).Once()
	for _, id := range []string{"pg-alpine", "pg-hub", "pg-digest", "supa", "my", "maria", "mongo"} {
		cli.On("ContainerInspect", mock.Anything, id).Return(container.InspectResponse{Config: &container.Config{}}, nil).Once()
	}

	databases, err := d.DiscoverDatabases(context.Background())

	assert.NoError(t, err)
	got := map[string]DatabaseType{}
	for _, db := range databases {
		got[db.ContainerID] = db.Type
	}
	assert.Equal(t, map[string]DatabaseType{
		"pg-alpine": DatabaseTypePostgreSQL, "pg-hub": DatabaseTypePostgreSQL, "pg-digest": DatabaseTypePostgreSQL,
		"supa": DatabaseTypePostgreSQL, "my": DatabaseTypeMySQL, "maria": DatabaseTypeMySQL, "mongo": DatabaseTypeMongoDB,
	}, got, "postgrest, another registry's postgres and Coolify's own database are not customer databases")
	cli.AssertExpectations(t)
}

// Selfhost creates a Coolify PostgreSQL with extensions on these images
// (PostgresqlStrategy / PostgresExtensions::Catalog): they are PostgreSQL too.
func TestDiscoverDatabases_FindsSelfhostsExtensionImages(t *testing.T) {
	cli := new(mockDockerClient)
	d := NewWithClient(cli)
	cli.On("ContainerList", mock.Anything, mock.Anything).Return([]container.Summary{
		{ID: "vector", Image: "pgvector/pgvector:pg17", Names: []string{"/dbuuid1"}},
		{ID: "tsdb", Image: "timescale/timescaledb:latest-pg17", Names: []string{"/dbuuid2"}},
		{ID: "tsdb-ha", Image: "timescale/timescaledb-ha:pg17", Names: []string{"/dbuuid3"}},
	}, nil).Once()
	for _, id := range []string{"vector", "tsdb", "tsdb-ha"} {
		cli.On("ContainerInspect", mock.Anything, id).Return(container.InspectResponse{Config: &container.Config{}}, nil).Once()
	}

	databases, err := d.DiscoverDatabases(context.Background())

	assert.NoError(t, err)
	assert.Len(t, databases, 3)
	for _, db := range databases {
		assert.Equal(t, DatabaseTypePostgreSQL, db.Type, db.ContainerID)
	}
}

// After a re-pull moves the tag, Docker lists the container's image as its
// ID; the name it was created from is still in its config.
func TestDiscoverDatabases_ImageListedAsAnIDFallsBackToItsConfiguredName(t *testing.T) {
	cli := new(mockDockerClient)
	d := NewWithClient(cli)
	cli.On("ContainerList", mock.Anything, mock.Anything).Return([]container.Summary{
		{ID: "pg", Image: "sha256:4f2b6e2a1c0d", Names: []string{"/dbuuid1"}},
	}, nil).Once()
	cli.On("ContainerInspect", mock.Anything, "pg").Return(container.InspectResponse{
		Config: &container.Config{Image: "postgres:17-alpine", Env: []string{"POSTGRES_USER=app"}},
	}, nil).Once()

	databases, err := d.DiscoverDatabases(context.Background())

	assert.NoError(t, err)
	if assert.Len(t, databases, 1) {
		assert.Equal(t, DatabaseTypePostgreSQL, databases[0].Type)
		assert.Equal(t, "app", databases[0].Username)
	}
	cli.AssertExpectations(t)
}

func TestImageRepository(t *testing.T) {
	for ref, want := range map[string]string{
		"postgres":                        "postgres",
		"postgres:17-alpine":              "postgres",
		"docker.io/library/postgres:16":   "postgres",
		"library/postgres:16":             "postgres",
		"postgres@sha256:abc":             "postgres",
		"postgres:17@sha256:abc":          "postgres",
		"supabase/postgres:15.8.1.060":    "supabase/postgres",
		"registry.local:5000/postgres:17": "postgres",
		"localhost/postgres":              "postgres",
		"ghcr.io/acme/postgres:1":         "acme/postgres",
		"postgrest/postgrest:v12.2.3":     "postgrest/postgrest",
		"mysql:8.4":                       "mysql",
	} {
		assert.Equal(t, want, imageRepository(ref), ref)
	}
}

// A container that cannot be inspected is skipped, and only that one: the
// rest are still found, and nothing is guessed for it.
func TestDiscoverDatabases_AnInspectFailureSkipsOnlyThatContainer(t *testing.T) {
	cli := new(mockDockerClient)
	d := NewWithClient(cli)
	cli.On("ContainerList", mock.Anything, mock.Anything).Return([]container.Summary{
		{ID: "gone", Image: "postgres:17-alpine", Names: []string{"/dbuuid1"}},
		{ID: "fine", Image: "postgres:17-alpine", Names: []string{"/dbuuid2"}},
	}, nil).Once()
	cli.On("ContainerInspect", mock.Anything, "gone").Return(container.InspectResponse{}, errors.New("No such container")).Once()
	cli.On("ContainerInspect", mock.Anything, "fine").Return(container.InspectResponse{Config: &container.Config{}}, nil).Once()

	databases, err := d.DiscoverDatabases(context.Background())

	assert.NoError(t, err)
	if assert.Len(t, databases, 1) {
		assert.Equal(t, "fine", databases[0].ContainerID)
	}
	cli.AssertExpectations(t)
}

// Coolify Redis (redis:8) is a customer database; Coolify's own coolify-redis
// queue is not.
func TestDiscoverDatabases_FindsRedisButNotCoolifysOwn(t *testing.T) {
	cli := new(mockDockerClient)
	d := NewWithClient(cli)
	cli.On("ContainerList", mock.Anything, mock.Anything).Return([]container.Summary{
		{ID: "cust", Image: "redis:8", Names: []string{"/dbuuid9"}},
		{ID: "own", Image: "redis:7-alpine", Names: []string{"/coolify-redis"}},
	}, nil).Once()
	cli.On("ContainerInspect", mock.Anything, "cust").Return(container.InspectResponse{
		Config: &container.Config{Env: []string{"REDIS_PASSWORD=rpw"}},
	}, nil).Once()

	databases, err := d.DiscoverDatabases(context.Background())

	assert.NoError(t, err)
	if assert.Len(t, databases, 1) {
		assert.Equal(t, DatabaseTypeRedis, databases[0].Type)
		assert.Equal(t, uint16(6379), databases[0].Port)
		assert.Equal(t, "rpw", databases[0].Password)
	}
	cli.AssertExpectations(t)
}
