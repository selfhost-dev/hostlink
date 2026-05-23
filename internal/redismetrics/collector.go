// Package redismetrics collects metrics from a Redis instance via INFO ALL.
package redismetrics

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"hostlink/domain/credential"
	"hostlink/domain/metrics"
)

type Collector interface {
	Collect(credential.Credential) (metrics.RedisMetrics, error)
}

type redisCollector struct {
	queryTimeout time.Duration
}

func New() Collector {
	return &redisCollector{queryTimeout: 10 * time.Second}
}

func (rc *redisCollector) Collect(cred credential.Credential) (metrics.RedisMetrics, error) {
	password := ""
	if cred.Password != nil {
		password = *cred.Password
	}

	opts := &redis.Options{
		Addr:         fmt.Sprintf("%s:%d", cred.Host, cred.Port),
		Password:     password,
		DB:           0,
		DialTimeout:  rc.queryTimeout,
		ReadTimeout:  rc.queryTimeout,
		WriteTimeout: rc.queryTimeout,
	}
	if cred.Username != "" {
		opts.Username = cred.Username
	}

	client := redis.NewClient(opts)
	defer client.Close() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), rc.queryTimeout)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return metrics.RedisMetrics{}, fmt.Errorf("ping: %w", err)
	}

	info, err := client.Info(ctx, "all").Result()
	if err != nil {
		return metrics.RedisMetrics{}, fmt.Errorf("INFO ALL: %w", err)
	}

	fields := parseInfo(info)

	m := metrics.RedisMetrics{
		ConnectedClients:       parseInt(fields["connected_clients"]),
		UsedMemoryBytes:        parseInt64(fields["used_memory"]),
		UsedMemoryRSSBytes:     parseInt64(fields["used_memory_rss"]),
		InstantaneousOpsPerSec: parseInt(fields["instantaneous_ops_per_sec"]),
		EvictedKeys:            parseInt64(fields["evicted_keys"]),
		ExpiredKeys:            parseInt64(fields["expired_keys"]),
		Role:                   fields["role"],
	}

	// Keyspace hit ratio
	hits := parseFloat64(fields["keyspace_hits"])
	misses := parseFloat64(fields["keyspace_misses"])
	if total := hits + misses; total > 0 {
		m.KeyspaceHitRatio = hits / total * 100
	}

	// Replication info — only relevant when this node is a replica
	if m.Role == "slave" || m.Role == "replica" {
		lag := parseInt(fields["master_last_io_seconds_ago"])
		m.ReplicationLagSeconds = &lag
		linked := fields["master_link_status"] == "up"
		m.ReplicationConnected = &linked
	}

	return m, nil
}

// parseInfo converts the Redis INFO string into a flat key→value map.
// Lines starting with '#' are section headers and are skipped.
func parseInfo(info string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(info, "\r\n") {
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		if idx := strings.IndexByte(line, ':'); idx >= 0 {
			result[line[:idx]] = strings.TrimSpace(line[idx+1:])
		}
	}
	return result
}

func parseInt(s string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(s))
	return v
}

func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return v
}

func parseFloat64(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}
