package live

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackielii/structpages"
)

// Fragment declares one component a page pushes: for a keyed signal of
// Topic, each admitted subscriber's stream renders Component for each id by
// calling the page's own handler, as that subscriber, with the id's element
// as HX-Target. Props is the only loader, and the page's middleware runs.
//
// Params gives the path parameters of the page's route for a key, as for
// structpages.URLFor (jobPage `/jobs/{jobId}`: map[string]any{"jobId": key});
// nil when the route has none.
type Fragment struct {
	Topic     string
	Component any // the method expression the element's id is generated from: jobsPage.Card
	Params    func(key string) map[string]any
}

// Pusher is implemented by a page struct whose components are pushed:
//
//	func (jobsPage) Live() []live.Fragment {
//		return []live.Fragment{{Topic: "orbit:job", Component: jobsPage.Card}}
//	}
type Pusher interface {
	Live() []Fragment
}

// pageFragment is a Fragment bound at mount to the page that declared it.
type pageFragment struct {
	Fragment
	page *structpages.PageNode
	next http.Handler
}

// Pages is a structpages middleware (WithMiddlewares) that finds, once per
// page at mount, the pages implementing Pusher and registers their fragments
// with h. It changes nothing in the page's handler. A push renders through
// the handler Pages was given, so every middleware after it in
// WithMiddlewares, and the page's own Middlewares, run for each fragment;
// register it first so they all do. Mount before serving.
func (h *Hub[V]) Pages(next http.Handler, pn *structpages.PageNode) http.Handler {
	p, ok := pn.Value.Interface().(Pusher)
	if !ok {
		return next
	}
	if pn.Method != http.MethodGet && pn.Method != "ALL" {
		slog.Warn("live: a page that pushes fragments must answer GET", "page", pn.Name, "method", pn.Method)
		return next
	}
	for _, f := range p.Live() {
		h.pages[f.Topic] = append(h.pages[f.Topic], pageFragment{f, pn, next})
	}
	return next
}

// renderPages renders f's page fragments for the subscriber whose stream
// request is r, as for a batch: one partial request per id, the batch's
// targets in the context so Load makes one query per component.
func (h *Hub[V]) renderPages(r *http.Request, f Frame, size *int) []Rendered {
	regs := h.pages[f.Topic]
	if len(regs) == 0 {
		return nil
	}
	ctx := r.Context()
	type job struct {
		reg    pageFragment
		req    *http.Request
		target string
	}
	var jobs []job
	byPage := map[*structpages.PageNode]*batch{}
	for _, id := range f.IDs {
		for _, reg := range regs {
			target, err := structpages.Key(ctx, reg.Component, id)
			if err != nil {
				slog.WarnContext(ctx, "live: fragment target id", "topic", f.Topic, "key", id, "err", err)
				continue
			}
			req, err := pageRequest(r, reg, id, target)
			if err != nil {
				slog.WarnContext(ctx, "live: fragment page URL", "topic", f.Topic, "target", target, "err", err)
				continue
			}
			t, err := h.cfg.Selector(req, reg.page)
			if err != nil {
				slog.WarnContext(ctx, "live: fragment target selection", "target", target, "err", err)
				continue
			}
			b := byPage[reg.page]
			if b == nil {
				b = &batch{loaded: map[string]any{}}
				byPage[reg.page] = b
			}
			b.targets = append(b.targets, t)
			jobs = append(jobs, job{reg, req, target})
		}
	}
	var out []Rendered
	for _, j := range jobs {
		if *size >= h.cfg.MaxFragment {
			break
		}
		rec := &recorder{header: http.Header{}, max: h.cfg.MaxFragment - *size}
		j.reg.next.ServeHTTP(rec, j.req.WithContext(context.WithValue(j.req.Context(), batchKey{}, byPage[j.reg.page])))
		switch {
		case rec.over:
		case rec.status() == http.StatusNotFound:
			out = append(out, Rendered{Target: j.target, Delete: true})
		case rec.status() != http.StatusOK || aborts(rec):
			// The signal still names the id: the tab pulls, and the page's
			// answer (403, a redirect, a 401) is handled as for any pull.
			slog.DebugContext(ctx, "live: fragment not pushed, the element will pull", "target", j.target, "status", rec.status())
		case rootID(rec.body.Bytes()) != j.target:
			slog.WarnContext(ctx, "live: fragment root is not the target", "topic", f.Topic, "target", j.target)
		default:
			*size += rec.body.Len()
			out = append(out, Rendered{Target: j.target, HTML: rec.body.String()})
		}
	}
	return out
}

// pageRequest is the subscriber's stream request r turned into an htmx
// partial GET of reg's page for key, keeping r's context, cookies and
// headers so the page authenticates the subscriber as for a pull.
func pageRequest(r *http.Request, reg pageFragment, key, target string) (*http.Request, error) {
	var params map[string]any
	if reg.Params != nil {
		params = reg.Params(key)
	}
	var args []any
	if params != nil {
		args = append(args, params)
	}
	u, err := structpages.URLFor(r.Context(), reg.page.Value.Interface(), args...)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header = r.Header.Clone()
	for _, k := range []string{"Accept", "Last-Event-ID", "Cache-Control", TargetsHeader} {
		req.Header.Del(k)
	}
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Request-Type", "partial")
	req.Header.Set("HX-Target", "#"+target)
	req.Host, req.RemoteAddr, req.TLS = r.Host, r.RemoteAddr, r.TLS
	for name, v := range params {
		req.SetPathValue(name, fmt.Sprint(v))
	}
	return req, nil
}
