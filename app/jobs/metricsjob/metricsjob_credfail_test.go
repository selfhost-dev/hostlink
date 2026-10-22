package metricsjob

import (
	"context"
	"errors"
	"testing"

	"hostlink/domain/credential"
)

// selfhost#3963 / hostlink#269: a failed credential fetch dropped the whole
// beat, system, network and storage with it, and the platform read the
// silence as a dead database.

func runBeats(t *testing.T, beats int, interval int, pusher *mockPusher, auth *mockAuthGetter, between func(beat int)) {
	t.Helper()
	trigger := func(ctx context.Context, fn func() error) {
		for i := 1; i <= beats; i++ {
			if between != nil {
				between(i)
			}
			if err := fn(); err != nil {
				t.Errorf("beat %d returned %v; a credential failure must not fail the beat", i, err)
			}
		}
	}
	job := NewJobWithConf(MetricsJobConfig{Trigger: trigger, CredFetchInterval: interval})
	cancel := job.Register(context.Background(), pusher, auth)
	defer cancel()
	job.Shutdown()
}

func TestCredFetchFailure_NoCacheStillPushesEveryBeat(t *testing.T) {
	pusher := &mockPusher{}
	auth := &mockAuthGetter{getErr: errors.New("context deadline exceeded")}

	runBeats(t, 3, 2, pusher, auth, nil)

	if len(pusher.pushCalls) != 3 {
		t.Fatalf("pushes = %d, want 3: system, network and storage need no credential", len(pusher.pushCalls))
	}
	for i, c := range pusher.pushCalls {
		if c.Dialect != "" {
			t.Errorf("beat %d pushed with dialect %q; with no credential the database set is skipped", i+1, c.Dialect)
		}
	}
}

func TestCredFetchFailure_KeepsLastGoodCredential(t *testing.T) {
	pusher := &mockPusher{}
	auth := &mockAuthGetter{creds: []credential.Credential{{Dialect: "postgresql", Host: "cached-host"}}}

	runBeats(t, 4, 1, pusher, auth, func(beat int) {
		if beat == 2 {
			auth.creds, auth.getErr = nil, errors.New("context deadline exceeded")
		}
	})

	if len(pusher.pushCalls) != 4 {
		t.Fatalf("pushes = %d, want 4", len(pusher.pushCalls))
	}
	for i, c := range pusher.pushCalls {
		if c.Dialect != "postgresql" || c.Host != "cached-host" {
			t.Errorf("beat %d pushed %q/%q, want the last good postgresql/cached-host", i+1, c.Dialect, c.Host)
		}
	}
}

func TestCredFetchFailure_RecoversWhenFetchSucceedsAgain(t *testing.T) {
	pusher := &mockPusher{}
	auth := &mockAuthGetter{getErr: errors.New("context deadline exceeded")}

	runBeats(t, 3, 2, pusher, auth, func(beat int) {
		if beat == 3 {
			auth.creds, auth.getErr = []credential.Credential{{Dialect: "mysql", Host: "db"}}, nil
		}
	})

	if len(pusher.pushCalls) != 3 {
		t.Fatalf("pushes = %d, want 3", len(pusher.pushCalls))
	}
	if got := pusher.pushCalls[2]; got.Dialect != "mysql" {
		t.Errorf("beat 3 pushed dialect %q, want mysql once the fetch succeeds", got.Dialect)
	}
}
