package leader

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		c       Config
		wantErr string
	}{
		{
			name:    "missing identity",
			c:       Config{Namespace: "ns", Name: "n"},
			wantErr: "Identity",
		},
		{
			name:    "missing namespace",
			c:       Config{Identity: "id", Name: "n"},
			wantErr: "Namespace",
		},
		{
			name:    "missing name",
			c:       Config{Identity: "id", Namespace: "ns"},
			wantErr: "Name",
		},
		{
			name: "renew >= lease",
			c: Config{
				Identity: "id", Namespace: "ns", Name: "n",
				LeaseDuration: 5 * time.Second,
				RenewDeadline: 10 * time.Second,
			},
			wantErr: "RenewDeadline",
		},
		{
			name: "ok",
			c:    Config{Identity: "id", Namespace: "ns", Name: "n"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.c.Validate()
			if c.wantErr == "" {
				if err != nil {
					t.Errorf("expected nil error, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("expected error containing %q, got %v", c.wantErr, err)
			}
		})
	}
}

func TestStatusString(t *testing.T) {
	if got := StatusLeader.String(); got != "leader" {
		t.Errorf("StatusLeader=%q, want leader", got)
	}
	if got := StatusFollower.String(); got != "follower" {
		t.Errorf("StatusFollower=%q, want follower", got)
	}
}

func TestAlwaysLeaderRunsCallback(t *testing.T) {
	a := NewAlwaysLeader()
	if a.IsLeader() {
		t.Error("AlwaysLeader should be follower until Run starts")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	leaderCh := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = a.Run(ctx, Callbacks{
			OnLeader: func(c context.Context) {
				close(leaderCh)
				<-c.Done()
			},
		})
		close(done)
	}()

	select {
	case <-leaderCh:
	case <-time.After(time.Second):
		t.Fatal("OnLeader never invoked")
	}
	if !a.IsLeader() {
		t.Error("AlwaysLeader.IsLeader should be true while Run is executing")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
	if a.IsLeader() {
		t.Error("AlwaysLeader.IsLeader should be false after Run returns")
	}
}

func TestFakeDispatchesTransitions(t *testing.T) {
	f := NewFake()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		leaderCalls   atomic.Int32
		followerCalls atomic.Int32
		leaderCancels atomic.Int32
	)
	cb := Callbacks{
		OnLeader: func(c context.Context) {
			leaderCalls.Add(1)
			<-c.Done()
			leaderCancels.Add(1)
		},
		OnFollower: func(c context.Context) {
			followerCalls.Add(1)
			<-c.Done()
		},
	}
	done := make(chan struct{})
	go func() { _ = f.Run(ctx, cb); close(done) }()

	f.Promote()
	if !waitFor(time.Second, func() bool { return leaderCalls.Load() == 1 }) {
		t.Fatal("OnLeader not invoked after Promote")
	}
	if !f.IsLeader() {
		t.Error("Fake.IsLeader should be true after Promote")
	}

	f.Demote()
	if !waitFor(time.Second, func() bool {
		return leaderCancels.Load() == 1 && followerCalls.Load() == 1
	}) {
		t.Fatalf("transitions not dispatched: leaderCancels=%d followerCalls=%d",
			leaderCancels.Load(), followerCalls.Load())
	}
	if f.IsLeader() {
		t.Error("Fake.IsLeader should be false after Demote")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Fake.Run did not return after ctx cancel")
	}
}

func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}
