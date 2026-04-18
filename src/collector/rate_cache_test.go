package collector

import (
	"sync"
	"testing"
	"time"
)

func TestRateCache_FirstSampleReturnsNotReady(t *testing.T) {
	rc := newRateCache()

	rate, ok := rc.observe("c1", "txn", 1000, time.Unix(100, 0))
	if ok {
		t.Errorf("first sample should not produce a rate, got %f", rate)
	}
}

func TestRateCache_SecondSampleReturnsDeltaPerSecond(t *testing.T) {
	rc := newRateCache()

	_, _ = rc.observe("c1", "txn", 1000, time.Unix(100, 0))
	rate, ok := rc.observe("c1", "txn", 2200, time.Unix(160, 0))

	if !ok {
		t.Fatal("expected rate available on second sample")
	}
	// (2200 - 1000) / (160 - 100) = 1200 / 60 = 20.0
	if rate != 20.0 {
		t.Errorf("expected rate=20.0, got %f", rate)
	}
}

func TestRateCache_CounterResetReturnsNotReady(t *testing.T) {
	// Postgres counters can reset (e.g. pg_stat_reset). We must not emit a
	// negative or wildly large rate when the value goes backwards; instead
	// drop the sample and start over.
	rc := newRateCache()

	_, _ = rc.observe("c1", "txn", 5000, time.Unix(100, 0))
	rate, ok := rc.observe("c1", "txn", 1000, time.Unix(160, 0))

	if ok {
		t.Errorf("counter reset should not produce a rate, got %f", rate)
	}

	// next sample should compute against the post-reset baseline
	rate, ok = rc.observe("c1", "txn", 1600, time.Unix(220, 0))
	if !ok {
		t.Fatal("expected rate after reset baseline established")
	}
	if rate != 10.0 {
		t.Errorf("expected rate=10.0 after reset, got %f", rate)
	}
}

func TestRateCache_ZeroElapsedReturnsNotReady(t *testing.T) {
	rc := newRateCache()

	_, _ = rc.observe("c1", "txn", 1000, time.Unix(100, 0))
	rate, ok := rc.observe("c1", "txn", 2000, time.Unix(100, 0))

	if ok {
		t.Errorf("zero elapsed should not produce a rate, got %f", rate)
	}
}

func TestRateCache_PerClusterPerKeyIsolation(t *testing.T) {
	rc := newRateCache()

	_, _ = rc.observe("c1", "txn", 100, time.Unix(0, 0))
	_, _ = rc.observe("c2", "txn", 500, time.Unix(0, 0))

	r1, ok1 := rc.observe("c1", "txn", 160, time.Unix(60, 0))
	r2, ok2 := rc.observe("c2", "txn", 800, time.Unix(60, 0))

	if !ok1 || r1 != 1.0 {
		t.Errorf("c1 rate wrong: rate=%f ok=%v", r1, ok1)
	}
	if !ok2 || r2 != 5.0 {
		t.Errorf("c2 rate wrong: rate=%f ok=%v", r2, ok2)
	}

	// Different key on same cluster should be independent.
	if _, ok := rc.observe("c1", "deadlock", 7, time.Unix(60, 0)); ok {
		t.Error("first deadlock sample should not produce a rate")
	}
}

func TestRateCache_ConcurrentSafe(t *testing.T) {
	rc := newRateCache()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = rc.observe("c1", "txn", int64(i*10), time.Unix(int64(i), 0))
		}(i)
	}
	wg.Wait()
}
