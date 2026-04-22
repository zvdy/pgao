package db

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func testLogger() *logrus.Logger {
	l := logrus.New()
	l.SetOutput(io.Discard)
	return l
}

func TestSupervisorConfigBackoffDoublesThenSaturates(t *testing.T) {
	cfg := SupervisorConfig{
		MinBackoff: time.Second,
		MaxBackoff: 8 * time.Second,
	}
	cases := []struct {
		n    int
		want time.Duration
	}{
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 8 * time.Second},
		{50, 8 * time.Second},
	}
	for _, c := range cases {
		if got := cfg.nextBackoff(c.n); got != c.want {
			t.Errorf("nextBackoff(%d)=%s, want %s", c.n, got, c.want)
		}
	}
}

func TestSupervisorConfigWithDefaults(t *testing.T) {
	cfg := SupervisorConfig{}.withDefaults()
	if cfg.HealthInterval != 30*time.Second ||
		cfg.MinBackoff != 5*time.Second ||
		cfg.MaxBackoff != 60*time.Second ||
		cfg.ProbeTimeout != 5*time.Second {
		t.Errorf("defaults wrong: %+v", cfg)
	}
}

// probeFakeProbe is a swappable fake probe that returns whatever err is set
// to. Used to drive the supervisor without a live PostgreSQL.
type probeFake struct {
	mu    sync.Mutex
	err   error
	calls int32
}

func (p *probeFake) setErr(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.err = err
}

func (p *probeFake) probe(_ context.Context, _ string) error {
	atomic.AddInt32(&p.calls, 1)
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

func TestSupervisorTransitionsHealthyOnSuccessfulProbe(t *testing.T) {
	cp := NewConnectionPool(testLogger())
	// Pre-register a cluster in the status map so we don't need to build
	// a real pgxpool. probe fake never touches the pool.
	cp.status.ensure("c1")

	fake := &probeFake{}
	cp.SetProbe(fake.probe)
	cp.SetSupervisorConfig(SupervisorConfig{
		HealthInterval: 10 * time.Millisecond,
		MinBackoff:     2 * time.Millisecond,
		MaxBackoff:     8 * time.Millisecond,
		ProbeTimeout:   50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() { cp.Supervise(ctx); close(done) }()

	if !waitFor(200*time.Millisecond, func() bool {
		st, ok := cp.GetState("c1")
		return ok && st.State == StateHealthy
	}) {
		t.Fatalf("cluster never became healthy")
	}
	cancel()
	<-done
}

func TestSupervisorTransitionsUnhealthyThenRecoversWithBackoff(t *testing.T) {
	cp := NewConnectionPool(testLogger())
	cp.status.ensure("c1")

	fake := &probeFake{err: errors.New("connection refused")}
	cp.SetProbe(fake.probe)
	cp.SetSupervisorConfig(SupervisorConfig{
		HealthInterval: 10 * time.Millisecond,
		MinBackoff:     2 * time.Millisecond,
		MaxBackoff:     20 * time.Millisecond,
		ProbeTimeout:   50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() { cp.Supervise(ctx); close(done) }()

	// Wait until the supervisor marks the cluster unhealthy after at least
	// two failed probes.
	if !waitFor(300*time.Millisecond, func() bool {
		st, ok := cp.GetState("c1")
		return ok && st.State == StateUnhealthy && st.FailedProbes >= 2
	}) {
		st, _ := cp.GetState("c1")
		t.Fatalf("cluster never went unhealthy with >= 2 failures: %+v", st)
	}
	st, _ := cp.GetState("c1")
	if st.LastError == "" {
		t.Errorf("expected LastError to surface the probe error, got empty")
	}

	// Flip the probe to success; cluster should recover.
	fake.setErr(nil)
	if !waitFor(400*time.Millisecond, func() bool {
		st, ok := cp.GetState("c1")
		return ok && st.State == StateHealthy && st.FailedProbes == 0
	}) {
		st, _ := cp.GetState("c1")
		t.Fatalf("cluster never recovered: %+v", st)
	}
	cancel()
	<-done
}

func TestStatusMapTransitionResetsFailureCountOnRecovery(t *testing.T) {
	s := newStatusMap()
	s.ensure("c1")

	now := time.Now()
	s.transition("c1", false, errors.New("boom"), now, 10*time.Millisecond)
	s.transition("c1", false, errors.New("boom"), now, 10*time.Millisecond)
	if st, _ := s.snapshot("c1"); st.FailedProbes != 2 {
		t.Fatalf("expected 2 failures, got %d", st.FailedProbes)
	}

	s.transition("c1", true, nil, now, 30*time.Second)
	st, _ := s.snapshot("c1")
	if st.State != StateHealthy || st.FailedProbes != 0 || st.LastError != "" {
		t.Errorf("recovery did not reset state: %+v", st)
	}
}

// waitFor polls cond until it returns true or the deadline elapses.
func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return cond()
}
