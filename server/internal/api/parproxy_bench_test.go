package api

import (
	"net/http/httptest"
	"testing"
)

// BenchmarkParProxy runs 16 goroutines per GOMAXPROCS against one service,
// so the per service pick lock and the single writer are contended.
func BenchmarkParProxy(b *testing.B) {
	e := newHPEnv(b)
	h := e.api.Router()
	b.ReportAllocs()
	b.SetParallelism(16)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest("GET", "/proxy/gen/v1/small", nil)
			req.Header.Set("Authorization", "Bearer "+e.token)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != 200 {
				b.Errorf("status %d", rec.Code)
				return
			}
		}
	})
}
