// Package metricsjob
package metricsjob

import (
	"context"
	"hostlink/app/services/metrics"
	"hostlink/domain/credential"
	"sync"
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
	var lastPgCred credential.Credential
	var beatCount int
	mj.wg.Add(1)
	go func() {
		defer mj.wg.Done()
		mj.config.Trigger(ctx, func() (err error) {
			beatCount++

			shouldFetch := len(creds) == 0 ||
				(mj.config.CredFetchInterval > 0 && beatCount%mj.config.CredFetchInterval == 0)

			if shouldFetch {
				creds, err = mcred.GetCreds()
				if err != nil {
					return err
				}

				lastPgCred = credential.Credential{}
				for _, cred := range creds {
					if cred.Dialect == "postgresql" {
						lastPgCred = cred
						break
					}
				}
			}

			return mp.Push(lastPgCred)
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
