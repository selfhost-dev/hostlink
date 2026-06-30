// Package heartbeatjob
package heartbeatjob

import (
	"context"
	"sync"

	"hostlink/app/services/heartbeat"
	"hostlink/domain/task"
	"hostlink/internal/telemetry"

	"github.com/labstack/gommon/log"
)

type TriggerFunc func(context.Context, func() error)

type TaskEnqueuer interface {
	Enqueue(ctx context.Context, t task.Task) error
}

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

func (hj *HeartbeatJob) Register(ctx context.Context, svc heartbeat.Service, enqueuers ...TaskEnqueuer) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	hj.cancel = cancel

	hj.wg.Add(1)
	go func() {
		defer hj.wg.Done()
		hj.config.Trigger(ctx, func() error {
			pendingTasks, err := svc.Send()
			if err != nil {
				return err
			}
			hj.enqueueTasks(ctx, pendingTasks, enqueuers...)
			return nil
		})
	}()

	return cancel
}

func (hj *HeartbeatJob) enqueueTasks(ctx context.Context, tasks []task.Task, enqueuers ...TaskEnqueuer) {
	if len(tasks) == 0 || len(enqueuers) == 0 {
		return
	}
	for _, t := range tasks {
		if t.Status == "completed" || t.Status == "failed" || t.Status == "cancelled" {
			continue
		}
		for _, enq := range enqueuers {
			if err := enq.Enqueue(ctx, t); err != nil {
				log.Errorf("failed to enqueue task %s from heartbeat: %v", t.ID, err)
			}
		}
	}
	telemetry.Metric("hostlink.heartbeat.tasks_delivered", len(tasks), map[string]any{})
}

func (hj *HeartbeatJob) Shutdown() {
	if hj.cancel != nil {
		hj.cancel()
	}
	hj.wg.Wait()
}
