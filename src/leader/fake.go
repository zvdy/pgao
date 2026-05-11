package leader

import (
	"context"
	"sync"
)

// AlwaysLeader is the no-op Elector used when leader_election.enabled
// is false. Run immediately invokes Callbacks.OnLeader and never demotes
// — single-replica deployments behave exactly as they did before
// leader election was introduced.
type AlwaysLeader struct {
	status statusRef
}

// NewAlwaysLeader returns an Elector that promotes the caller to
// leader for the lifetime of Run.
func NewAlwaysLeader() *AlwaysLeader {
	a := &AlwaysLeader{}
	a.status.set(StatusFollower)
	return a
}

// Run promotes the caller to leader and blocks until ctx is cancelled.
// The OnLeader callback runs in this goroutine with the parent ctx.
// OnFollower is never invoked (we only demote on shutdown, which the
// caller already handles via its own ctx cancellation).
func (a *AlwaysLeader) Run(ctx context.Context, cb Callbacks) error {
	a.status.set(StatusLeader)
	defer a.status.set(StatusFollower)
	if cb.OnLeader != nil {
		cb.OnLeader(ctx)
	} else {
		<-ctx.Done()
	}
	return nil
}

// IsLeader returns true while Run is executing.
func (a *AlwaysLeader) IsLeader() bool { return a.status.isLeader() }

// Fake is a controllable Elector used by tests to drive leadership
// transitions without a k8s API server. Tests call Promote / Demote;
// Run dispatches the appropriate callbacks. Each transition cancels
// the previous callback's context before invoking the new one, so
// goroutines spawned in callbacks observe Done() promptly.
type Fake struct {
	mu          sync.Mutex
	status      statusRef
	transitions chan Status
	cancel      context.CancelFunc
}

// NewFake builds a controllable Elector.
func NewFake() *Fake {
	f := &Fake{transitions: make(chan Status, 8)}
	f.status.set(StatusFollower)
	return f
}

// Promote schedules a follower→leader transition. Safe to call before
// Run starts; the transition will be observed once Run enters its
// dispatch loop.
func (f *Fake) Promote() { f.transitions <- StatusLeader }

// Demote schedules a leader→follower transition.
func (f *Fake) Demote() { f.transitions <- StatusFollower }

// IsLeader reports the most recently dispatched state.
func (f *Fake) IsLeader() bool { return f.status.isLeader() }

// Run dispatches transitions to the supplied callbacks until ctx is
// cancelled. Each callback runs in its own goroutine; when Run
// transitions away from a state, the previous callback's child
// context is cancelled.
func (f *Fake) Run(ctx context.Context, cb Callbacks) error {
	for {
		select {
		case <-ctx.Done():
			f.mu.Lock()
			if f.cancel != nil {
				f.cancel()
				f.cancel = nil
			}
			f.mu.Unlock()
			return nil
		case s := <-f.transitions:
			f.mu.Lock()
			if f.cancel != nil {
				f.cancel()
			}
			childCtx, childCancel := context.WithCancel(ctx)
			f.cancel = childCancel
			f.status.set(s)
			f.mu.Unlock()
			switch s {
			case StatusLeader:
				if cb.OnLeader != nil {
					go cb.OnLeader(childCtx)
				}
			case StatusFollower:
				if cb.OnFollower != nil {
					go cb.OnFollower(childCtx)
				}
			}
		}
	}
}
