package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPerIPBlocksAfterLimitAndResetsAfterWindow(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	limited := PerIP(2, 50*time.Millisecond)(ok)

	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		r.RemoteAddr = "203.0.113.5:12345"
		return r
	}

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		limited.ServeHTTP(rec, req())
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	limited.ServeHTTP(rec, req())
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected the 3rd request within the window to be rate limited, got %d", rec.Code)
	}

	// A different client IP is unaffected.
	otherReq := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	otherReq.RemoteAddr = "203.0.113.9:54321"
	rec = httptest.NewRecorder()
	limited.ServeHTTP(rec, otherReq)
	if rec.Code != http.StatusOK {
		t.Errorf("expected a different client IP to have its own limit, got %d", rec.Code)
	}

	time.Sleep(60 * time.Millisecond)
	rec = httptest.NewRecorder()
	limited.ServeHTTP(rec, req())
	if rec.Code != http.StatusOK {
		t.Errorf("expected the limit to reset after the window elapsed, got %d", rec.Code)
	}
}
