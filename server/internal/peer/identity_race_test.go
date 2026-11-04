package peer

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"sync"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// TestKeysFirstUseRace: callers that create the identity together must all
// get the identity that ends up stored. Before insert-if-absent, a later
// writer overwrote the key an earlier caller had already read back.
func TestKeysFirstUseRace(t *testing.T) {
	const trials, callers = 10, 8
	box, err := crypto.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	for trial := 0; trial < trials; trial++ {
		st, err := store.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, callers)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range callers {
			wg.Go(func() {
				<-start
				priv, err := Keys(context.Background(), st, box)
				if err != nil {
					t.Error(err)
					return
				}
				got[i] = ServerID(priv.Public().(ed25519.PublicKey))
			})
		}
		close(start)
		wg.Wait()
		final, err := Keys(context.Background(), st, box)
		if err != nil {
			t.Fatal(err)
		}
		want := ServerID(final.Public().(ed25519.PublicKey))
		for i, g := range got {
			if g != want {
				st.Close()
				t.Fatalf("trial %d: caller %d got server id %s, stored is %s", trial, i, g, want)
			}
		}
		st.Close()
	}
}

// TestKeysReadErrorIsNotAMiss: a failed read returns an error instead of
// making a new identity that would replace the stored one.
func TestKeysReadErrorIsNotAMiss(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, _ := crypto.New(bytes.Repeat([]byte{7}, 32))
	first, err := Keys(context.Background(), st, box)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Keys(ctx, st, box); err == nil {
		t.Fatal("Keys with a failing read returned a key, want an error")
	}
	again, err := Keys(context.Background(), st, box)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Equal(again) {
		t.Fatal("the stored identity changed after a failed read")
	}
}
