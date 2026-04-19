package collector

import (
	"sync"
	"time"
)

// rateCache turns monotonically-increasing PostgreSQL counters (xact_commit,
// blks_read, deadlocks, ...) into per-second rates by remembering the previous
// sample per (clusterID, key).
//
// observe returns (rate, true) once two valid samples are available. It
// returns (0, false) on the first sample, on a counter reset (current < prev,
// e.g. after pg_stat_reset or a database restart), and on zero elapsed time.
// On reset the previous sample is replaced so the next call has a fresh
// baseline.
type rateCache struct {
	mu      sync.Mutex
	samples map[string]rateSample
}

type rateSample struct {
	at    time.Time
	value int64
}

func newRateCache() *rateCache {
	return &rateCache{samples: make(map[string]rateSample)}
}

func (rc *rateCache) observe(clusterID, key string, value int64, at time.Time) (float64, bool) {
	delta, elapsed, ok := rc.delta(clusterID, key, value, at)
	if !ok || elapsed <= 0 {
		return 0, false
	}
	return float64(delta) / elapsed, true
}

// observeDelta returns the absolute increase since the previous sample, useful
// for "events since last collection" counters where the rate-per-second
// representation would lose precision (deadlocks, errors).
func (rc *rateCache) observeDelta(clusterID, key string, value int64, at time.Time) (int64, bool) {
	delta, _, ok := rc.delta(clusterID, key, value, at)
	return delta, ok
}

func (rc *rateCache) delta(clusterID, key string, value int64, at time.Time) (int64, float64, bool) {
	cacheKey := clusterID + "\x00" + key

	rc.mu.Lock()
	defer rc.mu.Unlock()

	prev, ok := rc.samples[cacheKey]
	rc.samples[cacheKey] = rateSample{at: at, value: value}

	if !ok || value < prev.value {
		return 0, 0, false
	}
	return value - prev.value, at.Sub(prev.at).Seconds(), true
}
