package store

import (
	"context"
	"testing"
	"time"
)

func TestDecodePath(t *testing.T) {
	for in, want := range map[string]string{
		"/v1/files":          "/v1/files",
		"v1/files":           "/v1/files",
		"/v1//files/":        "/v1/files",
		"/v1/%66iles":        "/v1/files",
		"/v1%2Ffiles":        "/v1/files",
		"/v1%252Ffiles":      "/v1/files",
		"/v1%25252Ffiles":    "/v1/files",
		"/V1/Files":          "/v1/files",
		"/x/../v1/files":     "/v1/files",
		"/x/%2e%2e/v1/files": "/v1/files",
		"/v1/./files":        "/v1/files",
		"\\v1\\files":        "/v1/files",
		"/v1%5Cfiles":        "/v1/files",
		"/v1/files%00.json":  "/v1/files",
		"/../../v1/files":    "/v1/files",
		"":                   "/",
	} {
		if got := DecodePath(in); got != want {
			t.Errorf("DecodePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchDeny(t *testing.T) {
	rules := []DenyRule{
		{ID: 1, Method: "POST", Service: "openai", PathPrefix: "/v1/files"},
		{ID: 2, Method: "*", Host: "api.example.com", PathPrefix: "/admin"},
		{ID: 3, Method: "DELETE", PathPrefix: "/"},
		{ID: 4, Method: "*", Service: "tools", PathPrefix: "/tools/delete_*"},
	}
	cases := []struct {
		t    DenyTarget
		want int64
	}{
		{DenyTarget{Method: "POST", Slugs: []string{"openai"}, Path: "v1/files"}, 1},
		{DenyTarget{Method: "post", Slugs: []string{"OpenAI"}, Path: "v1/files/x"}, 1},
		{DenyTarget{Method: "POST", Slugs: []string{"openai"}, Path: "v1/files;a=b"}, 1},
		{DenyTarget{Method: "POST", Slugs: []string{"openai"}, Path: "v1;a=b/files"}, 1},
		{DenyTarget{Method: "POST", Slugs: []string{"openai"}, Path: "~uploads/v1/files"}, 1},
		{DenyTarget{Method: "POST", Slugs: []string{"llm", "openai"}, Path: "v1/files"}, 1},
		{DenyTarget{Method: "POST", Slugs: []string{"openai"}, Path: "v1/filesystem"}, 0},
		{DenyTarget{Method: "GET", Slugs: []string{"openai"}, Path: "v1/files"}, 0},
		{DenyTarget{Method: "POST", Slugs: []string{"anthropic"}, Path: "v1/files"}, 0},
		{DenyTarget{Method: "GET", Host: "API.example.com.", HostPath: "/Admin/users"}, 2},
		{DenyTarget{Method: "GET", Host: "api.example.com", HostPath: "/x/%2e%2e/admin"}, 2},
		{DenyTarget{Method: "GET", Host: "other.example.com", HostPath: "/admin"}, 0},
		{DenyTarget{Method: "DELETE", Slugs: []string{"x"}, Path: "anything"}, 3},
		{DenyTarget{Method: "POST", Slugs: []string{"tools"}, Path: "/tools/delete_file"}, 4},
		{DenyTarget{Method: "POST", Slugs: []string{"tools"}, Path: "/tools/read_file"}, 0},
	}
	for _, c := range cases {
		got := MatchDeny(rules, c.t)
		var id int64
		if got != nil {
			id = got.ID
		}
		if id != c.want {
			t.Errorf("%+v: rule %d, want %d", c.t, id, c.want)
		}
	}
}

func TestDenyRuleNormalize(t *testing.T) {
	r := DenyRule{Method: "post", Host: "API.Example.com.", PathPrefix: "/v1//Files/"}
	if err := r.Normalize(); err != nil || r.Method != "POST" || r.Host != "api.example.com" || r.PathPrefix != "/v1/files" {
		t.Fatalf("%+v %v", r, err)
	}
	r = DenyRule{PathPrefix: "/v1/file*"}
	if err := r.Normalize(); err != nil || r.Method != "*" || r.PathPrefix != "/v1/file*" {
		t.Fatalf("%+v %v", r, err)
	}
	for _, bad := range []DenyRule{{Method: "TRACE"}, {Service: "a", Host: "b"}, {Host: "a.io/x"}, {PathPrefix: "/a*b"}} {
		if err := bad.Normalize(); err == nil {
			t.Errorf("%+v: no error", bad)
		}
	}
}

func TestParseMeta(t *testing.T) {
	got, err := ParseMeta(" customer=cust_A , Workspace=w1 ")
	if err != nil || got["customer"] != "cust_A" || got["workspace"] != "w1" || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	for _, bad := range []string{"customer", "=x", "k=", "a=1,a=2", "a=1,b=2,c=3,d=4,e=5,f=6", "k=v v", "k=é", "k=a,b"} {
		if _, err := ParseMeta(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

// Token and tag usage are recorded with the app's, in one commit, and the
// server's day spend sums every app.
func TestScopedUsage(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	a, err := s.CreateApp(ctx, &App{Name: "a", Mode: "both", Active: true, TokenPrefix: "acn_x"}, "h1")
	if err != nil {
		t.Fatal(err)
	}
	rctx := WithAppToken(ctx, 7)
	SetUsageTags(rctx, map[string]string{"customer": "c1"})
	for i := 0; i < 3; i++ {
		if err := s.RecordAppUsage(rctx, a.ID, 0.25, false); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.RecordAppUsage(rctx, a.ID, 0, true) // a cache hit counts for neither
	now := time.Now()
	tu, _ := s.GetTokenUsage(ctx, 7, now)
	gu, _ := s.GetTagUsage(ctx, a.ID, "customer", "c1", now)
	if tu.TodayRequests != 3 || gu.TodayRequests != 3 || gu.TodayCost != 0.75 {
		t.Fatalf("token %+v tag %+v", tu, gu)
	}
	if spent, _ := s.ServerSpendToday(ctx, now); spent != 0.75 {
		t.Fatalf("server spend %v", spent)
	}
	rows, err := s.TagUsage(ctx, a.ID, 30, 10, now)
	if err != nil || len(rows) != 1 || rows[0].Requests != 3 {
		t.Fatalf("%+v %v", rows, err)
	}
}
