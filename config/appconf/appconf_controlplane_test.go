package appconf

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAPIPingIdleTimeout(t *testing.T) {
	for _, c := range []struct {
		env  string
		want time.Duration
	}{{"", 15 * time.Second}, {"30s", 30 * time.Second}, {"500ms", 2 * time.Second}, {"10m", 2 * time.Minute}, {"garbage", 15 * time.Second}} {
		t.Setenv("HOSTLINK_API_PING_IDLE_TIMEOUT", c.env)
		assert.Equal(t, c.want, APIPingIdleTimeout(), "env %q", c.env)
	}
}

func TestAPIPingTimeout(t *testing.T) {
	for _, c := range []struct {
		env  string
		want time.Duration
	}{{"", 5 * time.Second}, {"3s", 3 * time.Second}, {"100ms", time.Second}, {"1m", 30 * time.Second}, {"garbage", 5 * time.Second}} {
		t.Setenv("HOSTLINK_API_PING_TIMEOUT", c.env)
		assert.Equal(t, c.want, APIPingTimeout(), "env %q", c.env)
	}
}

func TestAPIRedialAfterTimeouts(t *testing.T) {
	for _, c := range []struct {
		env  string
		want int
	}{{"", 2}, {"4", 4}, {"0", 1}, {"50", 10}, {" 3 ", 3}, {"garbage", 2}} {
		t.Setenv("HOSTLINK_API_REDIAL_AFTER_TIMEOUTS", c.env)
		assert.Equal(t, c.want, APIRedialAfterTimeouts(), "env %q", c.env)
	}
}
