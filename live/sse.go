package live

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ServeSSE streams viewer's frames until the request ends or the hub ends
// the subscription (End). It opens with a `: connected` comment and a
// `resync` event, because frames published between the page render and this
// connection are lost; the dispatcher answers resync by refetching every live
// element and applying the current values it carries (Current). When the
// request carries ?since=<Since at render> and nothing was published since,
// the resync is skipped. The query stays the same on reconnect, so a
// reconnect after anything was published still resyncs.
//
// Fragments are rendered here, in the subscriber's own goroutine, as viewer
// and with r's context, cookies and headers; serve the stream from a
// structpages route of the same Mount so that context carries the page tree.
//
// A host with a durable source (an outbox with ids, replayed from
// Last-Event-ID) writes its own handler instead: replay its rows with
// WriteEvent, then Subscribe and select over its feed, Subscription.Ready and
// Subscription.Ended, writing frames with WriteFrame(w, f, h.Render(r,
// viewer, f)...) and resyncs with WriteResync. Topic frames carry no id, so
// they never move the browser's Last-Event-ID.
func (h *Hub[V]) ServeSSE(w http.ResponseWriter, r *http.Request, viewer V) error {
	sub := h.Subscribe(viewer)
	defer sub.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)
	write := func(f func() error) error {
		if err := rc.SetWriteDeadline(time.Now().Add(h.cfg.WriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		if err := f(); err != nil {
			return err
		}
		return rc.Flush()
	}

	ctx := r.Context()
	current := r.URL.Query().Get("since") == h.Since()
	if err := write(func() error {
		if _, err := io.WriteString(w, ": connected\n\n"); err != nil || current {
			return err
		}
		return WriteResync(w, h.CurrentValues(ctx, viewer))
	}); err != nil {
		return err
	}

	beat := time.NewTicker(h.cfg.Heartbeat)
	defer beat.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sub.Ended():
			return nil
		case <-beat.C:
			if err := write(func() error { _, err := io.WriteString(w, ": ping\n\n"); return err }); err != nil {
				return err
			}
		case <-sub.Ready():
			frames, resync := sub.Take()
			if err := write(func() error {
				if resync {
					return WriteResync(w, h.CurrentValues(ctx, viewer))
				}
				for _, f := range frames {
					if err := WriteFrame(w, f, h.Render(r, viewer, f)...); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				return err
			}
		}
	}
}

// WriteFrame writes a frame as an SSE event named after the topic, with data
// {"ids":[…]} ({} for the whole topic), {"values":{key:value,…}} for a value
// frame, and "fragments":[{"target":id,"html":…},…] beside the ids when
// fragments are given ({"target":id,"delete":true} for an element removed).
func WriteFrame(w io.Writer, f Frame, fragments ...Rendered) error {
	var data bytes.Buffer
	enc := json.NewEncoder(&data)
	// The data is read by JSON.parse, never placed in HTML: escaping <, >
	// and & would add five bytes to each in every fragment.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(struct {
		IDs       []string          `json:"ids,omitempty"`
		Values    map[string]string `json:"values,omitempty"`
		Fragments []Rendered        `json:"fragments,omitempty"`
	}{f.IDs, f.Values, fragments}); err != nil {
		return err
	}
	return WriteEvent(w, f.Topic, "", strings.TrimSuffix(data.String(), "\n"))
}

// WriteResync writes the resync event, with data {} or
// {"values":{topic:{key:value,…},…}} carrying the current values to apply.
func WriteResync(w io.Writer, values map[string]map[string]string) error {
	data, err := json.Marshal(struct {
		Values map[string]map[string]string `json:"values,omitempty"`
	}{values})
	if err != nil {
		return err
	}
	return WriteEvent(w, "resync", "", string(data))
}

// WriteEvent writes one SSE event. An empty id leaves the browser's
// Last-Event-ID unchanged. name and id must not contain newlines; data must be
// a single line.
func WriteEvent(w io.Writer, name, id, data string) error {
	var err error
	if id != "" {
		_, err = fmt.Fprintf(w, "id: %s\n", id)
	}
	if err == nil {
		_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
	}
	return err
}
