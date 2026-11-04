package store

import (
	"context"
	"fmt"
	"testing"
)

// readsEnv is a service with four keys and an app with one token. With
// restricted set, the first key is limited to the app.
func readsEnv(b *testing.B, restricted bool) (*Store, int64, string) {
	s, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	ctx := context.Background()
	sv, err := s.CreateService(ctx, &Service{Slug: "s", Name: "S", Provider: "generic", BaseURL: "https://x.example", AuthType: "bearer",
		AuthName: "Authorization", Strategy: "round_robin", CooldownSec: 1, MaxAttempts: 1})
	if err != nil {
		b.Fatal(err)
	}
	a, err := s.CreateApp(ctx, &App{Name: "bench", Mode: "both", Active: true, TokenPrefix: "acn_"}, "hash-bench")
	if err != nil {
		b.Fatal(err)
	}
	for i := range 4 {
		nk := NewKey{ServiceID: sv.ID, Label: fmt.Sprint("k", i), SecretEnc: []byte("c"), SecretFP: fmt.Sprint("fp", i), Hint: "x", Tier: "primary", Weight: 1, Priority: 100}
		if restricted && i == 0 {
			nk.Restricted, nk.AppIDs = true, []int64{a.ID}
		}
		if _, err := s.CreateKey(ctx, nk); err != nil {
			b.Fatal(err)
		}
	}
	return s, sv.ID, "hash-bench"
}

func BenchmarkReadsCandidateKeys(b *testing.B) {
	s, sid, _ := readsEnv(b, false)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.CandidateKeys(ctx, sid); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadsCandidateKeysRestricted(b *testing.B) {
	s, sid, _ := readsEnv(b, true)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.CandidateKeys(ctx, sid); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadsAppByToken(b *testing.B) {
	s, _, h := readsEnv(b, false)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := s.AppByToken(ctx, h); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadsAppByTokenParallel(b *testing.B) {
	s, _, h := readsEnv(b, false)
	ctx := context.Background()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, _, err := s.AppByToken(ctx, h); err != nil {
				b.Error(err)
				return
			}
		}
	})
}
