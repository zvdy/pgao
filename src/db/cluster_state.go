package db

import (
	"sync"
	"time"
)

// ClusterState describes the operational state of a registered cluster.
//
// Lifecycle:
//
//	Connecting → Healthy   (first successful probe)
//	Healthy   → Unhealthy  (probe fails, all retries exhausted)
//	Unhealthy → Healthy    (probe succeeds again)
//	any       → Degraded   (reserved for future use; not transitioned to yet)
//
// State is exposed via ConnectionPool.GetState so callers (Prometheus
// exporter, /ready handler) can render a per-cluster status without
// duplicating the probe logic.
type ClusterState string

const (
	StateConnecting ClusterState = "connecting"
	StateHealthy    ClusterState = "healthy"
	StateDegraded   ClusterState = "degraded"
	StateUnhealthy  ClusterState = "unhealthy"
)

// ClusterStatus is a read-only snapshot of a cluster's runtime state.
type ClusterStatus struct {
	State        ClusterState
	LastError    string
	LastProbe    time.Time
	NextProbe    time.Time
	FailedProbes int
}

// clusterStatus is the mutable counterpart held under ConnectionPool's mutex.
// Kept in its own file so the state machine is testable without dragging in
// pgxpool. The probe function is injected so tests can drive transitions
// without a live PostgreSQL.
type clusterStatus struct {
	state        ClusterState
	lastError    string
	lastProbe    time.Time
	nextProbe    time.Time
	failedProbes int
}

// SupervisorConfig governs the cadence of probes and the backoff schedule
// applied after a probe failure. Zero values fall back to the defaults
// documented per-field.
type SupervisorConfig struct {
	// HealthInterval is the probe cadence while the cluster is healthy.
	// Default 30 s.
	HealthInterval time.Duration
	// MinBackoff is the first backoff after a failed probe. Doubles up to
	// MaxBackoff. Default 5 s.
	MinBackoff time.Duration
	// MaxBackoff is the cap on the per-cluster reconnect backoff.
	// Default 60 s.
	MaxBackoff time.Duration
	// ProbeTimeout caps how long a single probe attempt may take.
	// Default 5 s.
	ProbeTimeout time.Duration
}

func (c SupervisorConfig) withDefaults() SupervisorConfig {
	if c.HealthInterval <= 0 {
		c.HealthInterval = 30 * time.Second
	}
	if c.MinBackoff <= 0 {
		c.MinBackoff = 5 * time.Second
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 60 * time.Second
	}
	if c.ProbeTimeout <= 0 {
		c.ProbeTimeout = 5 * time.Second
	}
	return c
}

// nextBackoff returns the next probe delay given the consecutive-failure
// count. The first failure (n=1) returns MinBackoff; each subsequent failure
// doubles the delay up to MaxBackoff.
func (c SupervisorConfig) nextBackoff(failedProbes int) time.Duration {
	if failedProbes <= 1 {
		return c.MinBackoff
	}
	const maxBackoffShift = 16
	if failedProbes-1 > maxBackoffShift {
		return c.MaxBackoff
	}
	d := c.MinBackoff << (failedProbes - 1)
	if d <= 0 || d > c.MaxBackoff {
		return c.MaxBackoff
	}
	return d
}

// statusMap is a tiny mutex-guarded map. Pulled out so tests can poke at the
// state without touching ConnectionPool.
type statusMap struct {
	mu sync.RWMutex
	m  map[string]*clusterStatus
}

func newStatusMap() *statusMap {
	return &statusMap{m: make(map[string]*clusterStatus)}
}

func (s *statusMap) snapshot(id string) (ClusterStatus, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.m[id]
	if !ok {
		return ClusterStatus{}, false
	}
	return ClusterStatus{
		State:        st.state,
		LastError:    st.lastError,
		LastProbe:    st.lastProbe,
		NextProbe:    st.nextProbe,
		FailedProbes: st.failedProbes,
	}, true
}

func (s *statusMap) all() map[string]ClusterStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]ClusterStatus, len(s.m))
	for id, st := range s.m {
		out[id] = ClusterStatus{
			State:        st.state,
			LastError:    st.lastError,
			LastProbe:    st.lastProbe,
			NextProbe:    st.nextProbe,
			FailedProbes: st.failedProbes,
		}
	}
	return out
}

func (s *statusMap) ensure(id string) *clusterStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.m[id]
	if !ok {
		st = &clusterStatus{state: StateConnecting}
		s.m[id] = st
	}
	return st
}

// transition applies a probe result. ok=true marks the cluster healthy and
// resets the failure counter; ok=false marks it unhealthy and bumps the
// counter so the supervisor can compute the next backoff.
func (s *statusMap) transition(id string, ok bool, err error, now time.Time, next time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, exists := s.m[id]
	if !exists {
		st = &clusterStatus{}
		s.m[id] = st
	}
	st.lastProbe = now
	st.nextProbe = now.Add(next)
	if ok {
		st.state = StateHealthy
		st.lastError = ""
		st.failedProbes = 0
		return
	}
	st.state = StateUnhealthy
	st.failedProbes++
	if err != nil {
		st.lastError = err.Error()
	}
}

func (s *statusMap) remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
}
