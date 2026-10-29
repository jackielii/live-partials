package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// serverTiming reports the handler's time to first byte as Server-Timing, so
// the page meter can separate render time from network and swap time.
func serverTiming(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/events") {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(&timedWriter{ResponseWriter: w, start: time.Now()}, r)
	})
}

type timedWriter struct {
	http.ResponseWriter
	start time.Time
	sent  bool
}

func (t *timedWriter) WriteHeader(code int) {
	if !t.sent {
		t.sent = true
		d := float64(time.Since(t.start).Microseconds()) / 1000
		t.Header().Set("Server-Timing", fmt.Sprintf("app;dur=%.2f", d))
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
