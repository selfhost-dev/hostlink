package metricsjob

import (
	"context"
	"errors"
	"hostlink/domain/credential"
	"testing"
	"time"
)

type mockPusher struct {
	pushCalls []credential.Credential
	pushErr   error
}

func (m *mockPusher) Push(cred credential.Credential) error {
	m.pushCalls = append(m.pushCalls, cred)
	return m.pushErr
}

type mockAuthGetter struct {
	creds  []credential.Credential
	getErr error
	calls  int
}

func (m *mockAuthGetter) GetCreds() ([]credential.Credential, error) {
	m.calls++
	return m.creds, m.getErr
}

// Synchronous trigger for testing
func syncTrigger(ctx context.Context, fn func() error) {
	_ = fn() // Execute once immediately
}

func TestRegister_FetchesCredsAndPushes(t *testing.T) {
	pusher := &mockPusher{}
	authGetter := &mockAuthGetter{
		creds: []credential.Credential{
			{Dialect: "postgresql", Host: "localhost"},
		},
	}

	job := NewJobWithConf(MetricsJobConfig{
		Trigger:           syncTrigger,
		CredFetchInterval: 2,
	})

	ctx := context.Background()
	cancel := job.Register(ctx, pusher, authGetter)
	defer cancel()

	job.Shutdown()

	if authGetter.calls != 1 {
		t.Errorf("expected 1 GetCreds call, got %d", authGetter.calls)
	}

	if len(pusher.pushCalls) != 1 {
		t.Fatalf("expected 1 Push call, got %d", len(pusher.pushCalls))
	}

	if pusher.pushCalls[0].Dialect != "postgresql" {
		t.Errorf("expected postgresql dialect, got %s", pusher.pushCalls[0].Dialect)
	}
}

func TestRegister_CachesCredsWithinThreshold(t *testing.T) {
	tests := []struct {
		name              string
		credFetchInterval int
		totalBeats        int
		expectedCredCalls int
		description       string
	}{
		{
			name:              "SkipCredFetchBeat=0_FetchOnce",
			credFetchInterval: 0,
			totalBeats:        5,
			expectedCredCalls: 1,
			description:       "When passed zero, function called once on beat 1 only",
		},
		{
			name:              "SkipCredFetchBeat=1_FetchEveryBeat",
			credFetchInterval: 1,
			totalBeats:        6,
			expectedCredCalls: 6,
			description:       "Should call every beat: 1,2,3,4,5,6",
		},
		{
			name:              "SkipCredFetchBeat=2_FetchEverySecondBeat",
			credFetchInterval: 2,
			totalBeats:        8,
			expectedCredCalls: 5, // Changed from 4
			description:       "Should call on beats 1,2,4,6,8",
		},
		{
			name:              "SkipCredFetchBeat=3_FetchEveryThirdBeat",
			credFetchInterval: 3,
			totalBeats:        9,
			expectedCredCalls: 4, // Changed from 3
			description:       "Should call on beats 1,3,6,9",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pusher := &mockPusher{}
			authGetter := &mockAuthGetter{
				creds: []credential.Credential{
					{Dialect: "postgresql"},
				},
			}

			mockTrigger := func(ctx context.Context, fn func() error) {
				for range tt.totalBeats {
					_ = fn()
				}
			}

			job := NewJobWithConf(MetricsJobConfig{
				Trigger:           mockTrigger,
				CredFetchInterval: tt.credFetchInterval,
			})

			ctx := context.Background()
			cancel := job.Register(ctx, pusher, authGetter)
			defer cancel()

			job.Shutdown()

			if authGetter.calls != tt.expectedCredCalls {
				t.Errorf("%s: expected %d GetCreds calls, got %d",
					tt.description, tt.expectedCredCalls, authGetter.calls)
			}

			// Verify Push was called same number of times as GetCreds
			if len(pusher.pushCalls) != tt.expectedCredCalls {
				t.Errorf("expected %d Push calls, got %d",
					tt.expectedCredCalls, len(pusher.pushCalls))
			}
		})
	}
}

func TestRegister_NoPushAfterEmptyCreds(t *testing.T) {
	tests := []struct {
		name              string
		credFetchInterval int
		totalBeats        int
		emptyCredsAfter   int
		expectedPushCalls int
		description       string
	}{
		{
			name:              "Interval=0_EmptyAfterFirstBeat",
			credFetchInterval: 0,
			totalBeats:        5,
			emptyCredsAfter:   0,
			expectedPushCalls: 1,
			description:       "With interval=0, creds emptied after beat 1, only 1 push (beat 1), no refetch",
		},
		{
			name:              "Interval=0_NeverEmpty",
			credFetchInterval: 0,
			totalBeats:        5,
			emptyCredsAfter:   -1,
			expectedPushCalls: 1,
			description:       "With interval=0, creds never emptied, only 1 push (beat 1), never refetch",
		},
		{
			name:              "Interval=1_EmptyAfterFirstBeat",
			credFetchInterval: 1,
			totalBeats:        5,
			emptyCredsAfter:   0,
			expectedPushCalls: 1,
			description:       "With interval=1, creds emptied after beat 1, only 1 push (beat 1)",
		},
		{
			name:              "Interval=2_EmptyAfterFirstBeat",
			credFetchInterval: 2,
			totalBeats:        5,
			emptyCredsAfter:   0,
			expectedPushCalls: 1,
			description:       "With interval=2, creds emptied after beat 1, only 1 push (beat 1)",
		},
		{
			name:              "Interval=3_EmptyAfterFirstBeat",
			credFetchInterval: 3,
			totalBeats:        6,
			emptyCredsAfter:   0,
			expectedPushCalls: 1,
			description:       "With interval=3, creds emptied after beat 1, only 1 push (beat 1)",
		},
		{
			name:              "Interval=1_EmptyAfterThirdBeat",
			credFetchInterval: 1,
			totalBeats:        5,
			emptyCredsAfter:   2,
			expectedPushCalls: 3,
			description:       "With interval=1, creds emptied after beat 3, pushes on beats 1,2,3",
		},
		{
			name:              "Interval=2_NeverEmpty",
			credFetchInterval: 2,
			totalBeats:        8,
			emptyCredsAfter:   -1,
			expectedPushCalls: 5,
			description:       "With interval=2, creds never emptied, pushes on beats 1,2,4,6,8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pusher := &mockPusher{}
			authGetter := &mockAuthGetter{
				creds: []credential.Credential{
					{Dialect: "postgresql", Host: "localhost"},
				},
			}

			stopTriggerForPush := func(ctx context.Context, fn func() error) {
				for i := range tt.totalBeats {
					_ = fn()
					if i == tt.emptyCredsAfter {
						// After specified beat, all subsequent calls return empty credentials
						authGetter.creds = []credential.Credential{}
					}
				}
			}

			job := NewJobWithConf(MetricsJobConfig{
				Trigger:           stopTriggerForPush,
				CredFetchInterval: tt.credFetchInterval,
			})

			ctx := context.Background()
			cancel := job.Register(ctx, pusher, authGetter)
			defer cancel()

			job.Shutdown()

			if len(pusher.pushCalls) != tt.expectedPushCalls {
				t.Errorf("%s: expected %d Push calls, got %d",
					tt.description, tt.expectedPushCalls, len(pusher.pushCalls))
			}
		})
	}
}

func TestRegister_HandlesGetCredsError(t *testing.T) {
	pusher := &mockPusher{}
	authGetter := &mockAuthGetter{
		getErr: errors.New("auth failure"),
	}

	job := NewJobWithConf(MetricsJobConfig{
		Trigger:           syncTrigger,
		CredFetchInterval: 2,
	})

	ctx := context.Background()
	cancel := job.Register(ctx, pusher, authGetter)
	defer cancel()

	job.Shutdown()

	// Should not push when GetCreds fails
	if len(pusher.pushCalls) != 0 {
		t.Errorf("expected 0 Push calls on error, got %d", len(pusher.pushCalls))
	}
}

func TestRegister_SkipsNonPostgresqlCreds(t *testing.T) {
	pusher := &mockPusher{}
	authGetter := &mockAuthGetter{
		creds: []credential.Credential{
			{Dialect: "mysql"},
			{Dialect: "sqlite"},
		},
	}

	job := NewJobWithConf(MetricsJobConfig{
		Trigger:           syncTrigger,
		CredFetchInterval: 2,
	})

	ctx := context.Background()
	cancel := job.Register(ctx, pusher, authGetter)
	defer cancel()

	job.Shutdown()

	// Push should be called with zero-value credential
	if len(pusher.pushCalls) != 1 {
		t.Errorf("expected 1 Push call, got %d", len(pusher.pushCalls))
	}

	if pusher.pushCalls[0].Dialect != "" {
		t.Errorf("expected empty dialect, got %s", pusher.pushCalls[0].Dialect)
	}
}

func TestRegister_ContextCancellation(t *testing.T) {
	pusher := &mockPusher{}
	authGetter := &mockAuthGetter{
		creds: []credential.Credential{{Dialect: "postgresql"}},
	}

	// Real trigger with short interval for testing
	job := NewJobWithConf(MetricsJobConfig{
		Trigger: func(ctx context.Context, fn func() error) {
			Trigger(ctx, fn)
		},
		CredFetchInterval: 2,
	})

	ctx := context.Background()
	cancel := job.Register(ctx, pusher, authGetter)

	// Let it run briefly
	time.Sleep(100 * time.Millisecond)

	cancel()
	job.Shutdown()

	// Should complete without hanging
}
