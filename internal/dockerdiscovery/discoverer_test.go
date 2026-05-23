package dockerdiscovery

import (
	"context"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type mockDockerClient struct {
	mock.Mock
}

func (m *mockDockerClient) ContainerList(ctx context.Context, opts container.ListOptions) ([]container.Summary, error) {
	args := m.Called(ctx, opts)
	return args.Get(0).([]container.Summary), args.Error(1)
}

func (m *mockDockerClient) ContainerInspect(ctx context.Context, containerID string) (container.InspectResponse, error) {
	args := m.Called(ctx, containerID)
	return args.Get(0).(container.InspectResponse), args.Error(1)
}

func (m *mockDockerClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *mockDockerClient) NegotiateAPIVersion(ctx context.Context) {
	m.Called(ctx)
}

func matchAncestor(image string) any {
	return mock.MatchedBy(func(opts container.ListOptions) bool {
		return opts.Filters.Match("ancestor", image)
	})
}

func setupEmptyAllImages(cli *mockDockerClient) {
	allImages := []string{"postgres", "supabase/postgres", "mysql", "mariadb", "mongo"}
	for _, img := range allImages {
		cli.On("ContainerList", mock.Anything, matchAncestor(img)).Return([]container.Summary{}, nil).Once()
	}
}

func TestDiscoverDatabases_NoContainers(t *testing.T) {
	cli := new(mockDockerClient)
	d := NewWithClient(cli)
	setupEmptyAllImages(cli)

	databases, err := d.DiscoverDatabases(context.Background())

	assert.NoError(t, err)
	assert.Empty(t, databases)
	cli.AssertExpectations(t)
}

func TestDiscoverDatabases_PostgresContainer(t *testing.T) {
	cli := new(mockDockerClient)
	d := NewWithClient(cli)

	containerID := "abc123def456"
	cli.On("ContainerList", mock.Anything, matchAncestor("postgres")).Return([]container.Summary{
		{ID: containerID, Names: []string{"/my-postgres"}},
	}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("supabase/postgres")).Return([]container.Summary{}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("mysql")).Return([]container.Summary{}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("mariadb")).Return([]container.Summary{}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("mongo")).Return([]container.Summary{}, nil).Once()

	cli.On("ContainerInspect", mock.Anything, containerID).Return(container.InspectResponse{
		NetworkSettings: &container.NetworkSettings{
			NetworkSettingsBase: container.NetworkSettingsBase{
				Ports: nat.PortMap{
					nat.Port("5432/tcp"): []nat.PortBinding{{HostPort: "15432"}},
				},
			},
		},
		Config: &container.Config{
			Env: []string{
				"POSTGRES_USER=customuser",
				"POSTGRES_DB=customdb",
				"POSTGRES_PASSWORD=secret123",
			},
		},
	}, nil)

	databases, err := d.DiscoverDatabases(context.Background())

	assert.NoError(t, err)
	assert.Len(t, databases, 1)
	assert.Equal(t, DatabaseTypePostgreSQL, databases[0].Type)
	assert.Equal(t, containerID, databases[0].ContainerID)
	assert.Equal(t, "my-postgres", databases[0].ContainerName)
	assert.Equal(t, "localhost", databases[0].Host)
	assert.Equal(t, uint16(15432), databases[0].Port)
	assert.Equal(t, "customdb", databases[0].Database)
	assert.Equal(t, "customuser", databases[0].Username)
	assert.Equal(t, "secret123", databases[0].Password)
	cli.AssertExpectations(t)
}

func TestDiscoverDatabases_SupabasePostgresContainer(t *testing.T) {
	cli := new(mockDockerClient)
	d := NewWithClient(cli)

	containerID := "sup123"
	cli.On("ContainerList", mock.Anything, matchAncestor("postgres")).Return([]container.Summary{}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("supabase/postgres")).Return([]container.Summary{
		{ID: containerID, Names: []string{"/supabase-db"}},
	}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("mysql")).Return([]container.Summary{}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("mariadb")).Return([]container.Summary{}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("mongo")).Return([]container.Summary{}, nil).Once()

	cli.On("ContainerInspect", mock.Anything, containerID).Return(container.InspectResponse{
		NetworkSettings: &container.NetworkSettings{
			NetworkSettingsBase: container.NetworkSettingsBase{
				Ports: nat.PortMap{
					nat.Port("5432/tcp"): []nat.PortBinding{{HostPort: "5432"}},
				},
			},
		},
		Config: &container.Config{
			Env: []string{
				"POSTGRES_USER=supabase_user",
				"POSTGRES_DB=supabase_db",
				"POSTGRES_PASSWORD=supabase_pass",
			},
		},
	}, nil)

	databases, err := d.DiscoverDatabases(context.Background())

	assert.NoError(t, err)
	assert.Len(t, databases, 1)
	assert.Equal(t, DatabaseTypePostgreSQL, databases[0].Type)
	assert.Equal(t, "supabase-db", databases[0].ContainerName)
	assert.Equal(t, "supabase_user", databases[0].Username)
	cli.AssertExpectations(t)
}

func TestDiscoverDatabases_MySQLContainer(t *testing.T) {
	cli := new(mockDockerClient)
	d := NewWithClient(cli)

	containerID := "mysql111"
	cli.On("ContainerList", mock.Anything, matchAncestor("postgres")).Return([]container.Summary{}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("supabase/postgres")).Return([]container.Summary{}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("mysql")).Return([]container.Summary{
		{ID: containerID, Names: []string{"/my-mysql"}},
	}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("mariadb")).Return([]container.Summary{}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("mongo")).Return([]container.Summary{}, nil).Once()

	cli.On("ContainerInspect", mock.Anything, containerID).Return(container.InspectResponse{
		NetworkSettings: &container.NetworkSettings{
			NetworkSettingsBase: container.NetworkSettingsBase{
				Ports: nat.PortMap{
					nat.Port("3306/tcp"): []nat.PortBinding{{HostPort: "3307"}},
				},
			},
		},
		Config: &container.Config{
			Env: []string{
				"MYSQL_ROOT_PASSWORD=rootpass",
				"MYSQL_DATABASE=mydb",
			},
		},
	}, nil)

	databases, err := d.DiscoverDatabases(context.Background())

	assert.NoError(t, err)
	assert.Len(t, databases, 1)
	assert.Equal(t, DatabaseTypeMySQL, databases[0].Type)
	assert.Equal(t, "my-mysql", databases[0].ContainerName)
	assert.Equal(t, "root", databases[0].Username)
	assert.Equal(t, "rootpass", databases[0].Password)
	assert.Equal(t, uint16(3307), databases[0].Port)
	cli.AssertExpectations(t)
}

func TestDiscoverDatabases_MongoDBContainer(t *testing.T) {
	cli := new(mockDockerClient)
	d := NewWithClient(cli)

	containerID := "mongo222"
	cli.On("ContainerList", mock.Anything, matchAncestor("postgres")).Return([]container.Summary{}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("supabase/postgres")).Return([]container.Summary{}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("mysql")).Return([]container.Summary{}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("mariadb")).Return([]container.Summary{}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("mongo")).Return([]container.Summary{
		{ID: containerID, Names: []string{"/my-mongo"}},
	}, nil).Once()

	cli.On("ContainerInspect", mock.Anything, containerID).Return(container.InspectResponse{
		NetworkSettings: &container.NetworkSettings{
			NetworkSettingsBase: container.NetworkSettingsBase{
				Ports: nat.PortMap{
					nat.Port("27017/tcp"): []nat.PortBinding{{HostPort: "27018"}},
				},
			},
		},
		Config: &container.Config{
			Env: []string{
				"MONGO_INITDB_ROOT_USERNAME=admin",
				"MONGO_INITDB_ROOT_PASSWORD=mongopass",
			},
		},
	}, nil)

	databases, err := d.DiscoverDatabases(context.Background())

	assert.NoError(t, err)
	assert.Len(t, databases, 1)
	assert.Equal(t, DatabaseTypeMongoDB, databases[0].Type)
	assert.Equal(t, "my-mongo", databases[0].ContainerName)
	assert.Equal(t, "admin", databases[0].Username)
	assert.Equal(t, "mongopass", databases[0].Password)
	assert.Equal(t, uint16(27018), databases[0].Port)
	cli.AssertExpectations(t)
}

func TestDiscoverDatabases_MultipleTypes(t *testing.T) {
	cli := new(mockDockerClient)
	d := NewWithClient(cli)

	cli.On("ContainerList", mock.Anything, matchAncestor("postgres")).Return([]container.Summary{
		{ID: "pg1", Names: []string{"/pg-one"}},
	}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("supabase/postgres")).Return([]container.Summary{}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("mysql")).Return([]container.Summary{
		{ID: "mysql1", Names: []string{"/mysql-one"}},
	}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("mariadb")).Return([]container.Summary{}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("mongo")).Return([]container.Summary{}, nil).Once()

	cli.On("ContainerInspect", mock.Anything, "pg1").Return(container.InspectResponse{
		NetworkSettings: &container.NetworkSettings{
			NetworkSettingsBase: container.NetworkSettingsBase{
				Ports: nat.PortMap{
					nat.Port("5432/tcp"): []nat.PortBinding{{HostPort: "5432"}},
				},
			},
		},
		Config: &container.Config{Env: []string{"POSTGRES_PASSWORD=pgpass"}},
	}, nil)
	cli.On("ContainerInspect", mock.Anything, "mysql1").Return(container.InspectResponse{
		NetworkSettings: &container.NetworkSettings{
			NetworkSettingsBase: container.NetworkSettingsBase{
				Ports: nat.PortMap{
					nat.Port("3306/tcp"): []nat.PortBinding{{HostPort: "3306"}},
				},
			},
		},
		Config: &container.Config{Env: []string{"MYSQL_ROOT_PASSWORD=mysqlpass"}},
	}, nil)

	databases, err := d.DiscoverDatabases(context.Background())

	assert.NoError(t, err)
	assert.Len(t, databases, 2)
	types := map[DatabaseType]bool{}
	for _, db := range databases {
		types[db.Type] = true
	}
	assert.True(t, types[DatabaseTypePostgreSQL])
	assert.True(t, types[DatabaseTypeMySQL])
	cli.AssertExpectations(t)
}

func TestDiscoverDatabases_FiltersCoolifyDB(t *testing.T) {
	cli := new(mockDockerClient)
	d := NewWithClient(cli)

	cli.On("ContainerList", mock.Anything, matchAncestor("postgres")).Return([]container.Summary{
		{ID: "111", Names: []string{"/coolify-db"}},
		{ID: "222", Names: []string{"/my-app-db"}},
	}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("supabase/postgres")).Return([]container.Summary{}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("mysql")).Return([]container.Summary{}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("mariadb")).Return([]container.Summary{}, nil).Once()
	cli.On("ContainerList", mock.Anything, matchAncestor("mongo")).Return([]container.Summary{}, nil).Once()

	cli.On("ContainerInspect", mock.Anything, "222").Return(container.InspectResponse{
		Config: &container.Config{Env: []string{"POSTGRES_PASSWORD=pass"}},
	}, nil)

	databases, err := d.DiscoverDatabases(context.Background())

	assert.NoError(t, err)
	assert.Len(t, databases, 1)
	assert.Equal(t, "my-app-db", databases[0].ContainerName)
	cli.AssertExpectations(t)
}

func TestDiscoverDatabases_DockerUnavailable(t *testing.T) {
	d := New()
	databases, err := d.DiscoverDatabases(context.Background())

	assert.NoError(t, err)
	assert.Empty(t, databases)
}

func TestEnvExtractors(t *testing.T) {
	t.Run("postgres defaults", func(t *testing.T) {
		u, p, d := extractPostgresEnv([]string{"SOME_VAR=val"})
		assert.Equal(t, "postgres", u)
		assert.Equal(t, "", p)
		assert.Equal(t, "postgres", d)
	})

	t.Run("postgres custom", func(t *testing.T) {
		u, p, d := extractPostgresEnv([]string{
			"POSTGRES_USER=appuser",
			"POSTGRES_PASSWORD=apppass",
			"POSTGRES_DB=appdb",
		})
		assert.Equal(t, "appuser", u)
		assert.Equal(t, "apppass", p)
		assert.Equal(t, "appdb", d)
	})

	t.Run("mysql root password", func(t *testing.T) {
		u, p, d := extractMySQLEnv([]string{
			"MYSQL_ROOT_PASSWORD=rootpass",
			"MYSQL_DATABASE=mydb",
		})
		assert.Equal(t, "root", u)
		assert.Equal(t, "rootpass", p)
		assert.Equal(t, "mydb", d)
	})

	t.Run("mongo", func(t *testing.T) {
		u, p, d := extractMongoEnv([]string{
			"MONGO_INITDB_ROOT_USERNAME=admin",
			"MONGO_INITDB_ROOT_PASSWORD=mongopass",
		})
		assert.Equal(t, "admin", u)
		assert.Equal(t, "mongopass", p)
		assert.Equal(t, "", d)
	})
}
