package store

import (
	"context"
	"errors"
	"testing"
)

func TestDeleteAndClearRuns(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	mk := func(status string) int64 {
		r, err := s.CreateRun(ctx, &AgentRun{Kind: "create_provider", Status: status, Title: status})
		if err != nil {
			t.Fatal(err)
		}
		s.AddStep(ctx, r.ID, "note", "step", "")
		return r.ID
	}
	failed, running, review, published, cancelled := mk("failed"), mk("running"), mk("needs_review"), mk("published"), mk("cancelled")

	if err := s.DeleteRun(ctx, running); !errors.Is(err, ErrRunBusy) {
		t.Fatalf("deleting a running run: got %v, want ErrRunBusy", err)
	}
	if err := s.DeleteRun(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting a missing run: got %v, want ErrNotFound", err)
	}
	if err := s.DeleteRun(ctx, failed); err != nil {
		t.Fatal(err)
	}
	if steps, _ := s.ListSteps(ctx, failed, 0); len(steps) != 0 {
		t.Fatalf("steps of a deleted run survived: %d", len(steps))
	}

	n, err := s.ClearRuns(ctx)
	if err != nil || n != 2 {
		t.Fatalf("ClearRuns = %d, %v; want 2 (published and cancelled)", n, err)
	}
	for _, id := range []int64{published, cancelled} {
		if _, err := s.GetRun(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("run %d should be gone", id)
		}
	}
	for _, id := range []int64{running, review} {
		if _, err := s.GetRun(ctx, id); err != nil {
			t.Fatalf("run %d should be kept: %v", id, err)
		}
	}
}
