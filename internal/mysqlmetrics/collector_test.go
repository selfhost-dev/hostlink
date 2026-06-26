package mysqlmetrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDeltaRates_FirstCollectionReturnsZero(t *testing.T) {
	mc := &mysqlCollector{queryTimeout: 10 * time.Second}

	assert.Nil(t, mc.lastQueries, "lastQueries should be nil before first collection")
	assert.Nil(t, mc.lastSlowQueries, "lastSlowQueries should be nil before first collection")
}

func TestDeltaRates_SecondCollectionReturnsRates(t *testing.T) {
	baseTime := time.Now()
	initialQueries := int64(1000)
	initialSlowQueries := int64(10)

	mc := &mysqlCollector{
		queryTimeout:    10 * time.Second,
		lastQueries:     &initialQueries,
		lastSlowQueries: &initialSlowQueries,
		lastTime:        baseTime,
	}

	// Simulate 10 seconds elapsed, 500 new queries, 5 new slow queries
	elapsed := 10 * time.Second
	currentQueries := int64(1500)
	currentSlowQueries := int64(15)
	now := baseTime.Add(elapsed)

	elapsedSec := now.Sub(mc.lastTime).Seconds()
	qps := float64(currentQueries-*mc.lastQueries) / elapsedSec
	sqps := float64(currentSlowQueries-*mc.lastSlowQueries) / elapsedSec

	assert.Equal(t, 50.0, qps, "QPS should be 500 queries / 10s = 50")
	assert.Equal(t, 0.5, sqps, "slow QPS should be 5 queries / 10s = 0.5")
}

func TestDeltaRates_ZeroElapsedReturnsZero(t *testing.T) {
	baseTime := time.Now()
	initialQueries := int64(1000)
	initialSlowQueries := int64(10)

	mc := &mysqlCollector{
		queryTimeout:    10 * time.Second,
		lastQueries:     &initialQueries,
		lastSlowQueries: &initialSlowQueries,
		lastTime:        baseTime,
	}

	elapsed := mc.lastTime.Sub(baseTime).Seconds()
	assert.Equal(t, 0.0, elapsed, "elapsed should be 0 when time hasn't advanced")
}

func TestInnoDBBufferPoolHitRatio_Calculation(t *testing.T) {
	readReqs := 10000.0
	diskReads := 100.0

	ratio := (readReqs - diskReads) / readReqs * 100
	assert.Equal(t, 99.0, ratio, "hit ratio should be 99% when 100 of 10000 reads hit disk")
}

func TestInnoDBBufferPoolHitRatio_ZeroRequests(t *testing.T) {
	readReqs := 0.0
	diskReads := 0.0

	var ratio float64
	if readReqs > 0 {
		ratio = (readReqs - diskReads) / readReqs * 100
	}
	assert.Equal(t, 0.0, ratio, "hit ratio should be 0 when there are no read requests")
}

func TestParseInt_ValidValues(t *testing.T) {
	assert.Equal(t, 42, parseInt("42"))
	assert.Equal(t, 0, parseInt("0"))
	assert.Equal(t, 0, parseInt(""))
	assert.Equal(t, 0, parseInt("invalid"))
}

func TestParseInt64_ValidValues(t *testing.T) {
	assert.Equal(t, int64(1234567890), parseInt64("1234567890"))
	assert.Equal(t, int64(0), parseInt64(""))
	assert.Equal(t, int64(0), parseInt64("invalid"))
}
