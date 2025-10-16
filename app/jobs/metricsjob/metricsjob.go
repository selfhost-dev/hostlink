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
	var beatCount int
	mj.wg.Add(1)
	go func() {
		defer mj.wg.Done()
		mj.config.Trigger(ctx, func() (err error) {
			beatCount++

			// Determine if we should fetch credentials this beat
			shouldFetch := len(creds) == 0 ||
				(mj.config.CredFetchInterval > 0 && beatCount%mj.config.CredFetchInterval == 0)

			if shouldFetch {
				creds, err = mcred.GetCreds()
				if err != nil {
					return err
				}

				if len(creds) == 0 {
					// no op, waiting for the creds to be available
					return nil
				}

				var pgcred credential.Credential
				for _, cred := range creds {
					// only support one db for now, first match will exit the finding of creds
					if cred.Dialect == "postgresql" {
						pgcred = cred
						break
					}
				}

				// Only push when we actually fetch credentials
				return mp.Push(pgcred)
			}

			// If we didn't fetch this beat, return nil (no push)
			return nil
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
