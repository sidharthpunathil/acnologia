package api

import (
	"context"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Events of a hidden key stay out of what admins see after the key is gone,
// whether the key goes with its service or with a peer link's keys.
func TestHiddenKeyEventsStayHiddenAfterKeyIsGone(t *testing.T) {
	for _, how := range []string{"delete service", "delete service keys"} {
		t.Run(how, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			admin := e.adminClient()
			var sv store.Service
			e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "serpapi", "slug": "vault", "base_url": e.upURL}, 201, &sv)
			var keys struct {
				Keys []store.Key `json:"keys"`
			}
			e.do(e.super, "POST", "/api/admin/services/"+itoa(int(sv.ID))+"/keys",
				map[string]any{"keys": []map[string]any{{"secret": "hidv0001", "hidden": true}}}, 201, &keys)
			hid := keys.Keys[0].ID
			since := time.Now().Add(-time.Minute)
			before, _ := e.st.CountEvents(ctx, since, false)
			spendBefore, _ := e.st.SpendSince(ctx, since, false)
			e.st.AddEvent(ctx, store.NewEvent{Kind: "rate_limited", ServiceID: sv.ID, KeyID: hid, Detail: "secret history", Cost: 5})

			if how == "delete service" {
				e.do(e.super, "DELETE", "/api/admin/services/"+itoa(int(sv.ID)), nil, 200, nil)
			} else if err := e.st.DeleteServiceKeys(ctx, sv.ID); err != nil {
				t.Fatal(err)
			}
			var evs struct {
				Events []store.Event `json:"events"`
			}
			e.do(admin, "GET", "/api/admin/events?key_id="+itoa(int(hid)), nil, 200, &evs)
			if len(evs.Events) != 0 {
				t.Fatalf("admin sees a gone hidden key's events: %+v", evs.Events)
			}
			if n, _ := e.st.CountEvents(ctx, since, false); n != before {
				t.Fatalf("event count changed from %d to %d", before, n)
			}
			if sp, _ := e.st.SpendSince(ctx, since, false); sp != spendBefore {
				t.Fatalf("spend changed from %v to %v", spendBefore, sp)
			}
		})
	}
}
