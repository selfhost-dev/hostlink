package metrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"hostlink/domain/credential"
	domainmetrics "hostlink/domain/metrics"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// selfhost#3963: a stalled credential fetch held every metrics beat for the
// client's full 30s. The fetch now has its own, shorter bound.

type stalledCredsAPI struct{ deadline time.Time }

func (s *stalledCredsAPI) GetMetricsCreds(ctx context.Context, _ string) ([]credential.Credential, error) {
	s.deadline, _ = ctx.Deadline()
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *stalledCredsAPI) PushMetrics(context.Context, domainmetrics.MetricPayload) error { return nil }

func stalledPusher(api *stalledCredsAPI, timeout time.Duration) *metricspusher {
	state := new(MockAgentState)
	state.On("GetAgentID").Return("agt_test")
	return &metricspusher{apiserver: api, agentstate: state, credFetchTimeout: timeout}
}

func TestGetCreds_StalledFetchGivesUpAtItsBound(t *testing.T) {
	mp := stalledPusher(&stalledCredsAPI{}, 50*time.Millisecond)

	started := time.Now()
	_, err := mp.GetCreds()

	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "got %v", err)
	assert.Less(t, time.Since(started), 2*time.Second)
}

func TestGetCreds_DefaultBoundComesFromSettings(t *testing.T) {
	t.Setenv("HOSTLINK_API_CREDENTIALS_TIMEOUT", "1s")
	api := &stalledCredsAPI{}
	mp := stalledPusher(api, 0)

	started := time.Now()
	_, err := mp.GetCreds()

	require.Error(t, err)
	assert.WithinDuration(t, started.Add(time.Second), api.deadline, 200*time.Millisecond)
}
