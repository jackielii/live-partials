package main

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackielii/live-partials/examples/labdata"
)

// serverTiming reports, at the response's first byte, the handler's time so
// far as Server-Timing and the store queries it made as X-Queries, so the
// page meter can separate render time from network and swap time.
func serverTiming(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/events") {
			next.ServeHTTP(w, r)
			return
		}
		ctx, queries := labdata.WithQueries(r.Context())
		next.ServeHTTP(&timedWriter{ResponseWriter: w, start: time.Now(), queries: queries}, r.WithContext(ctx))
	})
}

type timedWriter struct {
	http.ResponseWriter
	start   time.Time
	queries *atomic.Int32
	sent    bool
}

func (t *timedWriter) WriteHeader(code int) {
	if !t.sent {
		t.sent = true
		d := float64(time.Since(t.start).Microseconds()) / 1000
		t.Header().Set("Server-Timing", fmt.Sprintf("app;dur=%.2f", d))
		t.Header().Set("X-Queries", fmt.Sprint(t.queries.Load()))
	}
	t.ResponseWriter.WriteHeader(code)
}

func (t *timedWriter) Write(b []byte) (int, error) {
	if !t.sent {
		t.WriteHeader(http.StatusOK)
	}
	return t.ResponseWriter.Write(b)
}

func (t *timedWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }
