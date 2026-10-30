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

// meteredStream reports what the stream spent on each batch of frames it
// writes, for the page meter: after a flush that carried a frame, a
// lab-meter event with the time from the batch's write deadline (set before
// any fragment is rendered) to the flush, and the store queries the
// fragments made. The dispatcher ignores the event.
type meteredStream struct {
	http.ResponseWriter
	queries *atomic.Int32
	start   time.Time
	frame   bool
}

func (m *meteredStream) SetWriteDeadline(t time.Time) error {
	m.start = time.Now()
	return http.NewResponseController(m.ResponseWriter).SetWriteDeadline(t)
}

func (m *meteredStream) Write(b []byte) (int, error) {
	if strings.HasPrefix(string(b), "event: ") && !strings.HasPrefix(string(b), "event: resync") {
		m.frame = true
	}
	return m.ResponseWriter.Write(b)
}

func (m *meteredStream) FlushError() error {
	if m.frame {
		m.frame = false
		ms := float64(time.Since(m.start).Microseconds()) / 1000
		if _, err := fmt.Fprintf(m.ResponseWriter, "event: lab-meter\ndata: server %.3f queries %d\n\n", ms, m.queries.Swap(0)); err != nil {
			return err
		}
	}
	return http.NewResponseController(m.ResponseWriter).Flush()
}

func (m *meteredStream) Unwrap() http.ResponseWriter { return m.ResponseWriter }

// Flush is needed although Unwrap is there: structpages' buffered writer
// flushes the writer under it only when that is an http.Flusher.
func (t *timedWriter) Flush() { _ = http.NewResponseController(t.ResponseWriter).Flush() }
