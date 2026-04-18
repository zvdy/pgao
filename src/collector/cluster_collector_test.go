package collector

import (
	"io"
	"sync"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/zvdy/pgao/src/db"
	"github.com/zvdy/pgao/src/models"
)

func newCollector() *ClusterCollector {
	log := logrus.New()
	log.SetOutput(io.Discard)
	return NewClusterCollector(db.NewConnectionPool(log), log, 0)
}

func TestClusterCollector_RegisterAndGet(t *testing.T) {
	cc := newCollector()

	cluster := models.NewCluster("c1", "c1", "healthy", nil)
	cc.RegisterCluster(cluster)

	got, err := cc.GetCluster("c1")
	if err != nil {
		t.Fatalf("GetCluster: %v", err)
	}
	if got.ID != "c1" {
		t.Errorf("expected id c1, got %s", got.ID)
	}

	if _, err := cc.GetCluster("missing"); err == nil {
		t.Error("expected error for missing cluster")
	}
}

func TestClusterCollector_UnregisterRemoves(t *testing.T) {
	cc := newCollector()
	cc.RegisterCluster(models.NewCluster("c1", "c1", "healthy", nil))

	if err := cc.UnregisterCluster("c1"); err != nil {
		t.Fatalf("UnregisterCluster: %v", err)
	}
	if err := cc.UnregisterCluster("c1"); err == nil {
		t.Error("expected error unregistering non-existent cluster")
	}
}

// TestClusterCollector_Concurrent exercises the RWMutex added to fix issue #2.
// Must run clean under `go test -race`.
func TestClusterCollector_Concurrent(t *testing.T) {
	cc := newCollector()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			id := "c"
			if i%2 == 0 {
				id = "c2"
			}
			cc.RegisterCluster(models.NewCluster(id, id, "healthy", nil))
		}(i)
		go func() {
			defer wg.Done()
			_ = cc.GetAllClusters()
		}()
	}
	wg.Wait()
}
