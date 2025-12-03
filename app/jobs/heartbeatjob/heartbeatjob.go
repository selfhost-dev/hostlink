// Package heartbeatjob
package heartbeatjob

import (
	"context"
	"sync"

	"hostlink/app/services/heartbeat"
)

type TriggerFunc func(context.Context, func() error)

type HeartbeatJobConfig struct {
	Trigger TriggerFunc
}

type HeartbeatJob struct {
	config HeartbeatJobConfig
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func New() HeartbeatJob {
	return NewWithConfig(HeartbeatJobConfig{
		Trigger: Trigger,
	})
}

func NewWithConfig(cfg HeartbeatJobConfig) HeartbeatJob {
	if cfg.Trigger == nil {
		cfg.Trigger = Trigger
	}

	return HeartbeatJob{
		config: cfg,
	}
}

func (hj *HeartbeatJob) Register(ctx context.Context, svc heartbeat.Service) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	hj.cancel = cancel

	hj.wg.Add(1)
	go func() {
		defer hj.wg.Done()
		hj.config.Trigger(ctx, func() error {
			return svc.Send()
		})
	}()

	return cancel
}

func (hj *HeartbeatJob) Shutdown() {
	if hj.cancel != nil {
		hj.cancel()
	}
	hj.wg.Wait()
}
