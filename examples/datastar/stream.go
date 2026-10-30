package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gsxhq/gsx"
	"github.com/starfederation/datastar-go/datastar"

	"github.com/jackielii/live-partials/examples/labdata"
	"github.com/jackielii/live-partials/live"
)

// resync is the frame a page's push gets when the tab may have missed
// frames: on connect, when anything was published after the page rendered
// (or always, when the stream URL carries no ?since=), and when the hub's
// buffer overflowed.
const resync = "resync"

// push is what a page's stream hands its push function for one frame: the
// viewer, a store handle counting this frame's queries, and the patches
// gathered so far.
type push struct {
	ctx    context.Context
	viewer viewer
	store  *store
	events []func(*datastar.ServerSentEventGenerator) error
}

// patch morphs each node into the element with its id, one patch-elements
// event per node. Datastar parses an event's elements inside a <template>,
// where the first element sets the parsing mode, so a <tr> or <tbody>
// sharing an event with a non-table element would be dropped.
func (p *push) patch(nodes ...gsx.Node) error {
	for _, n := range nodes {
		var b bytes.Buffer
		if err := n.Render(p.ctx, &b); err != nil {
			return err
		}
		p.events = append(p.events, func(sse *datastar.ServerSentEventGenerator) error {
			return sse.PatchElements(b.String())
		})
	}
	return nil
}

// inner replaces the children of the element matching selector with nodes.
func (p *push) inner(selector string, nodes ...gsx.Node) error {
	var b bytes.Buffer
	for _, n := range nodes {
		if err := n.Render(p.ctx, &b); err != nil {
			return err
		}
	}
	p.events = append(p.events, func(sse *datastar.ServerSentEventGenerator) error {
		return sse.PatchElements(b.String(), datastar.WithSelector(selector), datastar.WithModeInner())
	})
	return nil
}

// remove deletes the element with id.
func (p *push) remove(id string) {
	p.events = append(p.events, func(sse *datastar.ServerSentEventGenerator) error {
		return sse.RemoveElementByID(id)
	})
}

// signals patches v into the page's signals.
func (p *push) signals(v any) {
	p.events = append(p.events, func(sse *datastar.ServerSentEventGenerator) error {
		return sse.MarshalAndPatchSignals(v)
	})
}

// streamCompression compresses the streams when set. The measurements
// leave it off, as the htmx stages compress nothing; the fan-out test
// measures it separately.
var streamCompression []datastar.CompressionOption

// pushFunc adds to p what frame f changed on the page.
type pushFunc func(p *push, f live.Frame) error

// serveStream serves one tab's page stream until the request ends. The
// shell's own live part (the sidebar) is pushed for every page; page pushes
// the rest. After each frame it sends a lab-meter event (ignored by
// Datastar) with the render time and queries, the stream's Server-Timing.
func serveStream(w http.ResponseWriter, r *http.Request, s *store, h *hub, v viewer, d *labdata.Dropper, page pushFunc) error {
	ctx, release, ok := d.Stream(r.Context())
	defer release()
	if !ok {
		http.Error(w, "restarting", http.StatusServiceUnavailable)
		return nil
	}
	sub := h.Subscribe(v)
	defer sub.Close()
	opts := []datastar.SSEOption{datastar.WithContext(ctx)}
	if len(streamCompression) > 0 {
		opts = append(opts, datastar.WithCompression(streamCompression...))
	}
	sse := datastar.NewSSE(w, r, opts...)

	send := func(frames []live.Frame) error {
		var queries atomic.Int32
		start := time.Now()
		p := &push{ctx: ctx, viewer: v, store: s.Counting(&queries)}
		for _, f := range frames {
			if err := shell(p, f); err != nil {
				return err
			}
			if err := page(p, f); err != nil {
				return err
			}
		}
		took := time.Since(start)
		for _, e := range p.events {
			if err := e(sse); err != nil {
				return err
			}
		}
		if len(p.events) == 0 {
			return nil
		}
		return sse.Send("lab-meter", []string{fmt.Sprintf("server %.3f queries %d", float64(took.Microseconds())/1000, queries.Load())})
	}

	if r.URL.Query().Get("since") != h.Since() {
		if err := send([]live.Frame{{Topic: resync}}); err != nil {
			return err
		}
	}
	beat := time.NewTicker(15 * time.Second)
	defer beat.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-beat.C:
			if err := sse.Send("lab-ping", []string{"-"}); err != nil {
				return nil
			}
		case <-sub.Ready():
			frames, lost := sub.Take()
			if lost {
				frames = []live.Frame{{Topic: resync}}
			}
			if err := send(frames); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
	}
}

// shell pushes the layout's live part, the sidebar's presence dots.
func shell(p *push, f live.Frame) error {
	switch f.Topic {
	case resync:
		return p.patch(sidebar(p.store.People()))
	case "lab:presence":
		if f.IDs == nil {
			return p.patch(sidebar(p.store.People()))
		}
		for _, id := range f.IDs {
			pr, err := p.store.Person(id)
			if err != nil {
				continue
			}
			if err := p.patch(dm(pr)); err != nil {
				return err
			}
		}
	}
	return nil
}

// serverTiming reports the handler's time to first byte as Server-Timing and
// its store queries as X-Queries, for the meter. Streams report per frame.
func serverTiming(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/updates") {
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
		t.Header().Set("Server-Timing", fmt.Sprintf("app;dur=%.2f", float64(time.Since(t.start).Microseconds())/1000))
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
