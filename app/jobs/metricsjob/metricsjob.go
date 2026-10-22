// Package metricsjob
package metricsjob

import (
	"context"
	"hostlink/app/services/metrics"
	"hostlink/domain/credential"
	"sync"

	"github.com/labstack/gommon/log"
)

type TriggerFunc func(context.Context, func() error)

type MetricsJobConfig struct {
	Trigger TriggerFunc
	// CredFetchInterval tells in what intervals to call the cred fetch API
	// When set to 0, it will call one and never calls it again
	CredFetchInterval int
}

type MetricsJob struct {
	config MetricsJobConfig
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func New() MetricsJob {
	return NewJobWithConf(MetricsJobConfig{
		Trigger:           Trigger,
		CredFetchInterval: 2,
	})
}

func NewJobWithConf(cfg MetricsJobConfig) MetricsJob {
	if cfg.Trigger == nil {
		cfg.Trigger = Trigger
	}

	return MetricsJob{
		config: cfg,
	}
}

func (mj *MetricsJob) Register(ctx context.Context, mp metrics.Pusher, mcred metrics.AuthGetter) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	mj.cancel = cancel
	var creds []credential.Credential
	var lastDbCred credential.Credential
	var beatCount int
	mj.wg.Add(1)
	go func() {
		defer mj.wg.Done()
		mj.config.Trigger(ctx, func() (err error) {
			beatCount++

			shouldFetch := len(creds) == 0 ||
				(mj.config.CredFetchInterval > 0 && beatCount%mj.config.CredFetchInterval == 0)

			if shouldFetch {
				// A failed fetch costs the database set at most: system,
				// network and storage need no credential, and the last good
				// credentials stay in use (selfhost#3963).
				fetched, fetchErr := mcred.GetCreds()
				if fetchErr != nil {
					log.Warnj(log.JSON{"event": "metrics_credentials_fetch_failed", "error": fetchErr.Error(), "cached_credentials": len(creds)})
				} else {
					creds = fetched
				}

				if fetchErr == nil && len(creds) > 0 {
					next, coolifyPostgres := singleDatabaseCred(creds)
					switch {
					case next.Dialect != "":
						lastDbCred = next
					case coolifyPostgres:
						// Only Coolify PostgreSQL: collected per container by
						// the Docker path, matched by uuid. This path dialled
						// localhost and sent a second, false up:false.
						lastDbCred = credential.Credential{}
					}
				}
			}

			return mp.Push(lastDbCred)
		})
	}()
	return cancel
}

func (mj *MetricsJob) Shutdown() {
	if mj.cancel != nil {
		mj.cancel()
	}
	mj.wg.Wait()
}

// singleDatabaseCred picks the credential for the single-database path: the
// first PostgreSQL one that is not a Coolify database (a managed instance),
// else the first other supported engine. When the list holds a Coolify
// PostgreSQL database, that engine fallback is not taken, as before, when the
// Coolify entry itself was picked: wherever it sits in the list.
func singleDatabaseCred(creds []credential.Credential) (credential.Credential, bool) {
	var other credential.Credential
	coolifyPostgres := false
	for _, cred := range creds {
		switch cred.Dialect {
		case "postgresql":
			if cred.IsCoolify() {
				coolifyPostgres = true
				continue
			}
			return cred, coolifyPostgres
		case "mysql", "mariadb", "mongodb", "redis", "clickhouse", "opensearch", "kafka":
			if other.Dialect == "" {
				other = cred
			}
		}
	}
	if coolifyPostgres {
		return credential.Credential{}, true
	}
	return other, false
}
