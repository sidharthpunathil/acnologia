package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// twoStepAppByToken is AppByToken as it was before the join: the token,
// then GetApp. The join must return exactly what it returned.
func twoStepAppByToken(s *Store, ctx context.Context, hash string) (*App, *AppToken, error) {
	t, err := scanAppToken(s.r.QueryRowContext(ctx, `SELECT `+appTokenCols+` FROM app_tokens t LEFT JOIN users u ON u.id=t.created_by
		WHERE t.hash=?`, hash))
	if err != nil {
		return nil, nil, err
	}
	a, err := s.GetApp(ctx, t.AppID)
	if err != nil {
		return nil, nil, err
	}
	a.TokenID = t.ID
	return a, t, nil
}

func TestAppByTokenMatchesTwoStepRead(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, "owner@example.com", "Owner", "x", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	// An inactive app with a daily budget, request limits and allowed services.
	a, err := s.CreateApp(ctx, &App{Name: "x", Mode: "both", Active: false, TokenPrefix: "acn_x", AllowedServices: []string{"a", "b"}}, "h-default")
	if err != nil {
		t.Fatal(err)
	}
	daily, dr := 5.0, int64(100)
	if err := s.SetAppLimits(ctx, a.ID, AppLimits{DailyBudget: &daily, DailyRequests: &dr}); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if _, err := s.CreateAppToken(ctx, a.ID, "expired", "p", "h-expired", u.ID, &past); err != nil {
		t.Fatal(err)
	}
	rev, err := s.CreateAppToken(ctx, a.ID, "revoked", "p", "h-revoked", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeAppToken(ctx, a.ID, rev.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAppToken(ctx, a.ID, "by user", "p", "h-user", u.ID, nil); err != nil {
		t.Fatal(err)
	}
	// An active app with a monthly budget and a legacy token, copied from
	// apps.token_hash the way migrateAppTokens does (no creator, a last use).
	b, err := s.CreateApp(ctx, &App{Name: "y", Mode: "proxy", Active: true, TokenPrefix: "acn_y"}, "h-b")
	if err != nil {
		t.Fatal(err)
	}
	monthly := 12.5
	if err := s.SetAppLimits(ctx, b.ID, AppLimits{MonthlyBudget: &monthly}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE apps SET last_used_at=? WHERE id=?`, time.Now().Unix(), b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO app_tokens(app_id,name,prefix,hash,created_at,last_used_at)
		SELECT id, 'Default', token_prefix || '...', 'h-legacy', created_at, last_used_at FROM apps WHERE id=?`, b.ID); err != nil {
		t.Fatal(err)
	}

	for _, h := range []string{"h-default", "h-expired", "h-revoked", "h-user", "h-b", "h-legacy", "unknown"} {
		wa, wt, werr := twoStepAppByToken(s, ctx, h)
		ga, gt, gerr := s.AppByToken(ctx, h)
		if !errors.Is(gerr, werr) {
			t.Fatalf("%s: error %v, two-step read gave %v", h, gerr, werr)
		}
		if !reflect.DeepEqual(ga, wa) || !reflect.DeepEqual(gt, wt) {
			t.Fatalf("%s: join read\n%+v\n%+v\ntwo-step read\n%+v\n%+v", h, ga, gt, wa, wt)
		}
		if h == "unknown" {
			if !errors.Is(gerr, ErrNotFound) {
				t.Fatalf("unknown hash: %v", gerr)
			}
			continue
		}
		if gerr != nil || ga.TokenID != gt.ID {
			t.Fatalf("%s: err %v, app token id %d, token id %d", h, gerr, ga.TokenID, gt.ID)
		}
	}
	// The fixture covers what it claims to.
	ga, _, _ := s.AppByToken(ctx, "h-default")
	if ga.Active || ga.Limits.DailyBudget == nil || len(ga.AllowedServices) != 2 || ga.TokenCount != 2 {
		t.Fatalf("app x read as %+v", ga)
	}
	_, lt, _ := s.AppByToken(ctx, "h-legacy")
	if lt.CreatedBy != nil || lt.LastUsedAt == nil {
		t.Fatalf("legacy token read as %+v", lt)
	}
	_, ut, _ := s.AppByToken(ctx, "h-user")
	if ut.CreatedByEmail != "owner@example.com" {
		t.Fatalf("token creator email %q", ut.CreatedByEmail)
	}
}
