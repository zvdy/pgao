package leader

import (
	"context"
	"fmt"

	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// kubernetesElector wraps client-go's leaderelection package. The
// Elector interface lets callers in main.go swap this for the Fake
// without taking a transitive dependency on client-go from tests that
// don't need it.
type kubernetesElector struct {
	cfg    Config
	client kubernetes.Interface
	status statusRef
}

// NewKubernetesElector builds an Elector backed by a coordination.k8s.io
// Lease. Reads the in-cluster service-account credentials, so the pod
// must have a Role granting get/list/watch/create/update on Leases in
// the configured namespace (the Helm chart ships this RBAC).
func NewKubernetesElector(cfg Config) (Elector, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	rcfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("leader: in-cluster config: %w", err)
	}
	client, err := kubernetes.NewForConfig(rcfg)
	if err != nil {
		return nil, fmt.Errorf("leader: build clientset: %w", err)
	}
	return newKubernetesElectorWithClient(cfg, client), nil
}

func newKubernetesElectorWithClient(cfg Config, client kubernetes.Interface) *kubernetesElector {
	cfg = cfg.withDefaults()
	e := &kubernetesElector{cfg: cfg, client: client}
	e.status.set(StatusFollower)
	return e
}

// Run starts the leader-election loop. Returns nil when ctx is
// cancelled. Errors during lease acquisition / renewal are not
// fatal — client-go's loop logs them and retries.
func (e *kubernetesElector) Run(ctx context.Context, cb Callbacks) error {
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      e.cfg.Name,
			Namespace: e.cfg.Namespace,
		},
		Client: e.client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: e.cfg.Identity,
		},
	}

	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   e.cfg.LeaseDuration,
		RenewDeadline:   e.cfg.RenewDeadline,
		RetryPeriod:     e.cfg.RetryPeriod,
		ReleaseOnCancel: true,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(c context.Context) {
				e.status.set(StatusLeader)
				if cb.OnLeader != nil {
					cb.OnLeader(c)
				}
			},
			OnStoppedLeading: func() {
				e.status.set(StatusFollower)
				if cb.OnFollower != nil {
					// client-go doesn't pass a context to OnStoppedLeading,
					// so we synthesise a cancelled one — followers shouldn't
					// be doing long-lived work anyway.
					ctx, cancel := context.WithCancel(context.Background())
					cancel()
					cb.OnFollower(ctx)
				}
			},
		},
	})
	return nil
}

// IsLeader reports whether this replica is currently the lease-holder.
func (e *kubernetesElector) IsLeader() bool { return e.status.isLeader() }

// Compile-time guard so the api package can rely on the wrapped Lease
// type matching our expected version.
var _ coordinationv1.Lease // keep the import so go mod tidy doesn't drop it
