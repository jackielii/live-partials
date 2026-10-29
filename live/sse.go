package live

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ServeSSE streams viewer's frames until the request ends. It opens with a
// `: connected` comment and a `resync` event, because frames published
// between the page render and this connection are lost; the dispatcher
// answers resync by refetching every live element. When the request carries
// ?since=<Since at render> and nothing was published since, the resync is
// skipped. The query stays the same on reconnect, so a reconnect after
// anything was published still resyncs.
//
// A host with a durable source (an outbox with ids, replayed from
// Last-Event-ID) writes its own handler instead: replay its rows with
// WriteEvent, then Subscribe and select over both its feed and
// Subscription.Ready, writing topic frames with WriteFrame. Topic frames carry
// no id, so they never move the browser's Last-Event-ID.
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

	current := r.URL.Query().Get("since") == h.Since()
	if err := write(func() error {
		if _, err := io.WriteString(w, ": connected\n\n"); err != nil || current {
			return err
		}
		return WriteEvent(w, "resync", "", "{}")
	}); err != nil {
		return err
	}

	beat := time.NewTicker(h.cfg.Heartbeat)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-beat.C:
			if err := write(func() error { _, err := io.WriteString(w, ": ping\n\n"); return err }); err != nil {
				return err
			}
		case <-sub.Ready():
			frames, resync := sub.Take()
			if err := write(func() error {
				if resync {
					return WriteEvent(w, "resync", "", "{}")
				}
				for _, f := range frames {
					if err := WriteFrame(w, f); err != nil {
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

// WriteFrame writes a topic frame as an SSE event named after the topic,
// with data {"ids":[…]} or {} for the whole topic.
func WriteFrame(w io.Writer, f Frame) error {
	data := []byte("{}")
	if len(f.IDs) > 0 {
		var err error
		if data, err = json.Marshal(struct {
			IDs []string `json:"ids"`
		}{f.IDs}); err != nil {
			return err
		}
	}
	return WriteEvent(w, f.Topic, "", string(data))
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
