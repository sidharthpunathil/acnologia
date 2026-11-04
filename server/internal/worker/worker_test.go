package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/pool"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// slowLoop stands in for a peer sync or sharing pass still writing when
// shutdown starts: it finishes a moment after ctx ends.
type slowLoop struct {
	started chan struct{}
	done    atomic.Bool
}

func (l *slowLoop) run(ctx context.Context) {
	select {
	case l.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	time.Sleep(100 * time.Millisecond)
	l.done.Store(true)
}

type peersFunc func(context.Context)

func (f peersFunc) SyncAll(ctx context.Context) { f(ctx) }

type tickFunc func(context.Context)

func (f tickFunc) Tick(ctx context.Context) { f(ctx) }

// TestRunWaitsForItsLoops: Run returns only after its child loops have, so
// the caller can close the store without cutting their writes.
func TestRunWaitsForItsLoops(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, err := crypto.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	peers := &slowLoop{started: make(chan struct{}, 1)}
	sharing := &slowLoop{started: make(chan struct{}, 1)}
	w := &Worker{Store: st, Pool: pool.New(st, box),
		Peers: peersFunc(peers.run), PeerInterval: time.Hour,
		Sharing: tickFunc(sharing.run), Refresh: tickFunc(func(context.Context) {})}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	<-peers.started
	<-sharing.started
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if !peers.done.Load() || !sharing.done.Load() {
		t.Fatalf("Run returned before its loops: peers done %v, sharing done %v", peers.done.Load(), sharing.done.Load())
	}
}
