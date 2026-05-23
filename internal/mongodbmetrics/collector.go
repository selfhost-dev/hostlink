// Package mongodbmetrics collects metrics from a MongoDB instance via serverStatus.
package mongodbmetrics

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"hostlink/domain/credential"
	"hostlink/domain/metrics"
)

type Collector interface {
	Collect(credential.Credential) (metrics.MongoDBMetrics, error)
}

// serverStatus is a partial decode of db.adminCommand({serverStatus:1}).
type serverStatus struct {
	Connections struct {
		Current   int32 `bson:"current"`
		Available int32 `bson:"available"`
	} `bson:"connections"`
	OpCounters struct {
		Insert int64 `bson:"insert"`
		Query  int64 `bson:"query"`
		Update int64 `bson:"update"`
		Delete int64 `bson:"delete"`
	} `bson:"opcounters"`
	Mem struct {
		Resident int32 `bson:"resident"`
	} `bson:"mem"`
}

type opSnapshot struct {
	insert, query, update, delete int64
}

type mongoCollector struct {
	queryTimeout time.Duration
	lastOps      *opSnapshot
	lastTime     time.Time
}

func New() Collector {
	return &mongoCollector{queryTimeout: 10 * time.Second}
}

func (mc *mongoCollector) Collect(cred credential.Credential) (metrics.MongoDBMetrics, error) {
	password := ""
	if cred.Password != nil {
		password = *cred.Password
	}

	var uri string
	if cred.Username != "" {
		uri = fmt.Sprintf(
			"mongodb://%s:%s@%s:%d/?authSource=admin&connectTimeoutMS=10000&serverSelectionTimeoutMS=10000",
			cred.Username, password, cred.Host, cred.Port,
		)
	} else {
		uri = fmt.Sprintf(
			"mongodb://%s:%d/?connectTimeoutMS=10000&serverSelectionTimeoutMS=10000",
			cred.Host, cred.Port,
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), mc.queryTimeout)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return metrics.MongoDBMetrics{}, fmt.Errorf("connect: %w", err)
	}
	defer client.Disconnect(ctx) //nolint:errcheck

	if err := client.Ping(ctx, nil); err != nil {
		return metrics.MongoDBMetrics{}, fmt.Errorf("ping: %w", err)
	}

	var status serverStatus
	result := client.Database("admin").RunCommand(ctx, bson.D{{Key: "serverStatus", Value: 1}})
	if err := result.Decode(&status); err != nil {
		return metrics.MongoDBMetrics{}, fmt.Errorf("serverStatus: %w", err)
	}

	m := metrics.MongoDBMetrics{
		ConnectionsCurrent:   int(status.Connections.Current),
		ConnectionsAvailable: int(status.Connections.Available),
		ResidentMemoryMB:     int(status.Mem.Resident),
	}

	// Delta-based ops/sec — opcounters are cumulative since mongod start
	now := time.Now()
	cur := &opSnapshot{
		insert: status.OpCounters.Insert,
		query:  status.OpCounters.Query,
		update: status.OpCounters.Update,
		delete: status.OpCounters.Delete,
	}
	if mc.lastOps != nil {
		elapsed := now.Sub(mc.lastTime).Seconds()
		if elapsed > 0 {
			di := float64(cur.insert-mc.lastOps.insert) / elapsed
			dq := float64(cur.query-mc.lastOps.query) / elapsed
			du := float64(cur.update-mc.lastOps.update) / elapsed
			dd := float64(cur.delete-mc.lastOps.delete) / elapsed
			m.InsertsPerSecond = di
			m.QueriesPerSecond = dq
			m.UpdatesPerSecond = du
			m.DeletesPerSecond = dd
			m.OpsPerSecond = di + dq + du + dd
		}
	}
	mc.lastOps = cur
	mc.lastTime = now

	return m, nil
}
