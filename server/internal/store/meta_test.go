package store

import (
	"context"
	"testing"
)

func TestLookupAndInsertMetaIfAbsent(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if v, found, err := s.LookupMeta(ctx, "k"); err != nil || found || v != "" {
		t.Fatalf("missing key: %q %v %v", v, found, err)
	}
	if err := s.InsertMetaIfAbsent(ctx, "k", "one"); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertMetaIfAbsent(ctx, "k", "two"); err != nil {
		t.Fatal(err)
	}
	if v, found, err := s.LookupMeta(ctx, "k"); err != nil || !found || v != "one" {
		t.Fatalf("after two inserts: %q %v %v, want the first value", v, found, err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := s.LookupMeta(cctx, "k"); err == nil {
		t.Fatal("a failed read returned no error")
	}
}
