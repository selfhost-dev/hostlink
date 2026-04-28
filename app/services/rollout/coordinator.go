package rollout

import (
	"sync"
	"time"
)

type Coordinator struct {
	mu                   sync.Mutex
	localDeliveryEnabled bool
	fallbackThreshold    time.Duration
	now                  func() time.Time
	effectiveDelivery    bool
	inactiveSince        *time.Time
}

func NewCoordinator(localDeliveryEnabled bool, fallbackThreshold time.Duration) *Coordinator {
	return NewCoordinatorWithClock(localDeliveryEnabled, fallbackThreshold, time.Now)
}

func NewCoordinatorWithClock(localDeliveryEnabled bool, fallbackThreshold time.Duration, now func() time.Time) *Coordinator {
	if now == nil {
		now = time.Now
	}
	return &Coordinator{localDeliveryEnabled: localDeliveryEnabled, fallbackThreshold: fallbackThreshold, now: now}
}

func (c *Coordinator) ShouldPoll() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.localDeliveryEnabled || !c.effectiveDelivery {
		return true
	}
	if c.inactiveSince == nil {
		return false
	}
	return c.now().Sub(*c.inactiveSince) >= c.fallbackThreshold
}

func (c *Coordinator) SetSessionDeliveryEnabled(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.effectiveDelivery = c.localDeliveryEnabled && enabled
	c.inactiveSince = nil
}

func (c *Coordinator) MarkSessionInactive() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.effectiveDelivery || c.inactiveSince != nil {
		return
	}
	inactiveSince := c.now()
	c.inactiveSince = &inactiveSince
}
