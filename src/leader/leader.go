// Package leader wraps Kubernetes lease-based leader election so pgao
// can run with replicaCount > 1 without every replica scraping every
// PostgreSQL cluster in parallel.
//
// The data plane (connection supervisor + collectors) only runs on the
// leader. Followers stay alive so they can take over quickly when the
// leader's lease expires; their /ready endpoint returns 503 so the
// kubernetes Service rotates them out.
//
// The Elector interface lets callers swap the real client-go
// implementation (NewKubernetesElector) for a Fake driven by tests.
package leader

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// Status reports the current leadership state. Safe to read from any
// goroutine.
type Status int32

const (
	// StatusFollower means this replica is alive but is not the leader.
	StatusFollower Status = iota
	// StatusLeader means this replica is currently the lease-holder
	// and should be running the data plane.
	StatusLeader
)

// String returns "follower" or "leader" for log messages.
func (s Status) String() string {
	if s == StatusLeader {
		return "leader"
	}
	return "follower"
}

// Config controls a single Elector instance.
type Config struct {
	// Identity uniquely identifies this replica. Must differ from every
	// other replica racing for the same Lease — typically the pod name
	// from POD_NAME (downward API) or os.Hostname.
	Identity string
	// Namespace is the k8s namespace the Lease lives in. Defaults to
	// the pod's namespace via the in-cluster service-account token.
	Namespace string
	// Name of the Lease object. Scope it to the release (e.g.
	// "{release}-pgao-leader") so co-tenanted releases don't fight.
	Name string
	// LeaseDuration is how long a captured lease is honoured before
	// peers can attempt to take it. Default 15 s; matches client-go's
	// recommended setting.
	LeaseDuration time.Duration
	// RenewDeadline is how long the leader has to renew before giving
	// up. Must be < LeaseDuration. Default 10 s.
	RenewDeadline time.Duration
	// RetryPeriod is the polling interval for both followers (waiting
	// to take over) and leaders (renewing). Default 2 s.
	RetryPeriod time.Duration
}

// withDefaults returns a copy of c with zero-valued fields filled in.
func (c Config) withDefaults() Config {
	if c.LeaseDuration <= 0 {
		c.LeaseDuration = 15 * time.Second
	}
	if c.RenewDeadline <= 0 {
		c.RenewDeadline = 10 * time.Second
	}
	if c.RetryPeriod <= 0 {
		c.RetryPeriod = 2 * time.Second
	}
	return c
}

// Validate reports configuration errors that would cause Run to fail
// at runtime. Surfaced eagerly so a misconfigured deployment errors at
// startup, not after a leadership transition.
func (c Config) Validate() error {
	if c.Identity == "" {
		return errors.New("leader: Identity is required")
	}
	if c.Namespace == "" {
		return errors.New("leader: Namespace is required")
	}
	if c.Name == "" {
		return errors.New("leader: Name is required")
	}
	cfg := c.withDefaults()
	if cfg.RenewDeadline >= cfg.LeaseDuration {
		return errors.New("leader: RenewDeadline must be < LeaseDuration")
	}
	return nil
}

// Callbacks is invoked by Run on leadership transitions. Both callbacks
// receive a context that is cancelled when the leadership state ends —
// OnLeader's ctx is cancelled when this replica is demoted; OnFollower's
// ctx is cancelled when this replica is promoted.
type Callbacks struct {
	OnLeader   func(ctx context.Context)
	OnFollower func(ctx context.Context)
}

// Elector is the surface main.go consumes. The k8s implementation
// (NewKubernetesElector) is in elector_k8s.go; the test fake is in
// fake.go.
type Elector interface {
	// Run blocks until ctx is cancelled. It invokes the supplied
	// callbacks on leadership transitions; both callbacks must be
	// non-blocking (typically they spawn a goroutine wrapped in a
	// WaitGroup). When ctx is cancelled the implementation drains
	// the active callback's child context and returns.
	Run(ctx context.Context, cb Callbacks) error

	// IsLeader returns the current leadership state.
	IsLeader() bool
}

// statusRef is a tiny atomic wrapper used by both the real and fake
// implementations so IsLeader is safe from any goroutine.
type statusRef struct {
	v atomic.Int32
}

func (s *statusRef) set(status Status) { s.v.Store(int32(status)) }
func (s *statusRef) get() Status       { return Status(s.v.Load()) }
func (s *statusRef) isLeader() bool    { return s.get() == StatusLeader }
