package live

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/jackielii/structpages"
)

// Component is what a FragmentFunc returns: anything that renders, such as
// a page's component called with its arguments (jobsPage{}.Card(view)) or
// any gsx.Node.
type Component interface {
	Render(ctx context.Context, w io.Writer) error
}

// FragmentFunc returns the component showing key, as viewer sees it. ctx is
// the stream request's, so it carries the page tree when the stream is served
// by a structpages route, and whatever the host's middleware put there. An
// error (ErrNotFound or the host's own) sends no fragment for key: the
// element is refetched as for a plain signal, and the page decides.
type FragmentFunc[V any] func(ctx context.Context, viewer V, key string) (Component, error)

// CurrentFunc returns a value topic's current values as viewer may see them.
// The stream sends them with every resync, so value listeners converge after
// frames were missed.
type CurrentFunc[V any] func(ctx context.Context, viewer V) (map[string]string, error)

type fragment[V any] struct {
	component any
	render    FragmentFunc[V]
}

// Fragment registers component as pushed on the stream for signals of topic,
// rendered by a function beside the page rather than through it (the hybrid
// stage; a page declaring Live and Pages is the alternative that keeps Props
// the only loader):
// for each id of a frame, each subscriber's stream calls render with its own
// viewer and sends the HTML, whose root element must carry the id
// structpages.Key(ctx, component, id). component is the method expression the
// element's id is generated from (jobsPage.Card). Register before serving.
//
// Only keyed frames carry fragments; an id-less frame and a resync stay
// signals. The element keeps its On attribute: it is refetched whenever no
// fragment arrives for it.
func (h *Hub[V]) Fragment(topic string, component any, render FragmentFunc[V]) {
	h.fragments[topic] = append(h.fragments[topic], fragment[V]{component, render})
}

// Current registers the source of a value topic's current values, sent with
// every resync. Register before serving.
func (h *Hub[V]) Current(topic string, current CurrentFunc[V]) {
	h.current[topic] = current
}

// Rendered is one element of a fragment frame: its HTML, or Delete when the
// page answered 404.
type Rendered struct {
	Target string `json:"target"` // the element's id
	HTML   string `json:"html,omitempty"`
	Delete bool   `json:"delete,omitempty"`
}

// Render renders f's fragments for the subscriber whose stream request is r
// and whose viewer is viewer: those of pages declaring Live (through the
// page, see Pages) and those registered with Fragment. A fragment that fails
// to render, is not 200 (404 deletes the element), or whose root element is
// not its target is left out, as is any past Config.MaxFragment bytes; the
// dispatcher refetches those elements.
func (h *Hub[V]) Render(r *http.Request, viewer V, f Frame) []Rendered {
	if f.Values != nil || len(f.IDs) == 0 {
		return nil
	}
	size := 0
	out := h.renderPages(r, f, &size)
	regs := h.fragments[f.Topic]
	if len(regs) == 0 {
		return out
	}
	ctx := r.Context()
	for _, id := range f.IDs {
		for _, reg := range regs {
			target, err := structpages.Key(ctx, reg.component, id)
			if err != nil {
				slog.WarnContext(ctx, "live: fragment target id", "topic", f.Topic, "key", id, "err", err)
				continue
			}
			c, err := reg.render(ctx, viewer, id)
			if err == nil {
				var b bytes.Buffer
				if err = c.Render(ctx, &b); err == nil {
					switch {
					case rootID(b.Bytes()) != target:
						slog.WarnContext(ctx, "live: fragment root is not the target", "topic", f.Topic, "target", target)
					case size+b.Len() > h.cfg.MaxFragment:
					default:
						size += b.Len()
						out = append(out, Rendered{Target: target, HTML: b.String()})
					}
					continue
				}
			}
			if !errors.Is(err, ErrNotFound) {
				slog.WarnContext(ctx, "live: fragment not rendered, the element will pull", "topic", f.Topic, "target", target, "err", err)
			}
		}
	}
	return out
}

// CurrentValues returns every registered value topic's current values for
// viewer, for a resync. A source that fails is left out and logged.
func (h *Hub[V]) CurrentValues(ctx context.Context, viewer V) map[string]map[string]string {
	if len(h.current) == 0 {
		return nil
	}
	out := map[string]map[string]string{}
	for topic, current := range h.current {
		v, err := current(ctx, viewer)
		if err != nil {
			slog.WarnContext(ctx, "live: current values", "topic", topic, "err", err)
			continue
		}
		out[topic] = v
	}
	return out
}
