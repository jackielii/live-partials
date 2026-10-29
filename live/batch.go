package live

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackielii/structpages"
	nethtml "golang.org/x/net/html"
)

// TargetsHeader is the request header in which the dispatcher lists, space
// separated, the ids of the elements it wants re-rendered by the page.
const TargetsHeader = "X-Live-Targets"

// Batch configures Middleware. Zero fields take the defaults in brackets.
type Batch struct {
	// Selector must be the TargetSelector given to Mount; Load uses it to
	// learn which component each requested id names [HTMXv4RenderTarget].
	Selector   structpages.TargetSelector
	MaxTargets int // ids accepted per request; more is a 400 [64]
	MaxBytes   int // total response body; targets past it are named in Live-Skipped [1 MiB]
}

// Middleware is Batch{}.Middleware.
func Middleware(next http.Handler, pn *structpages.PageNode) http.Handler {
	return Batch{}.Middleware(next, pn)
}

// Middleware is a structpages middleware (WithMiddlewares) that answers a GET
// carrying X-Live-Targets with every listed element in one response. It calls
// next once per id, as an htmx partial request with HX-Target "#<id>", so
// Props sees one target and one key, as for any partial. Each body becomes
// <hx-partial hx-target="#id" hx-swap="outerMorph">; a 404 becomes a partial
// that deletes the element. Any other failure (or a body whose root element
// is not the target) skips that target only, and is logged.
//
// A redirect, a 401, or an HX-Refresh, HX-Redirect or HX-Location header
// from any target aborts the batch: that response is sent as it is. HX-Trigger
// headers from every target are merged into the response on every path.
//
// Middleware outside it runs once per batch; middleware inside it (a page's
// Middlewares) runs once per target.
func (b Batch) Middleware(next http.Handler, pn *structpages.PageNode) http.Handler {
	if b.Selector == nil {
		b.Selector = structpages.HTMXv4RenderTarget
	}
	if b.MaxTargets == 0 {
		b.MaxTargets = 64
	}
	if b.MaxBytes == 0 {
		b.MaxBytes = 1 << 20
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		list := r.Header.Get(TargetsHeader)
		if list == "" || r.Method != http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		b.serve(w, r, next, pn, strings.Fields(list))
	})
}

func (b Batch) serve(w http.ResponseWriter, r *http.Request, next http.Handler, pn *structpages.PageNode, ids []string) {
	w.Header().Set("Vary", TargetsHeader)
	w.Header().Set("Cache-Control", "no-store")
	if len(ids) > b.MaxTargets {
		http.Error(w, fmt.Sprintf("live: at most %d targets", b.MaxTargets), http.StatusBadRequest)
		return
	}

	bt := &batch{loaded: map[string]any{}}
	for _, id := range ids {
		t, err := b.Selector(partialRequest(r, id), pn)
		if err != nil {
			slog.WarnContext(r.Context(), "live: target selection failed", "target", id, "err", err)
			continue
		}
		bt.targets = append(bt.targets, t)
	}
	ctx := context.WithValue(r.Context(), batchKey{}, bt)

	var body bytes.Buffer
	var triggers, skipped []string
	for _, id := range ids {
		if body.Len() >= b.MaxBytes {
			skipped = append(skipped, id)
			continue
		}
		rec := &recorder{header: http.Header{}, max: b.MaxBytes - body.Len()}
		next.ServeHTTP(rec, partialRequest(r.WithContext(ctx), id))
		triggers = append(triggers, rec.header.Values("HX-Trigger")...)

		if aborts(rec) {
			for k, v := range rec.header {
				w.Header()[k] = v
			}
			setTrigger(w, r, triggers)
			w.WriteHeader(rec.status())
			w.Write(rec.body.Bytes())
			return
		}
		attr := html.EscapeString("#" + id)
		switch {
		case rec.over:
			skipped = append(skipped, id)
		case rec.status() == http.StatusNotFound:
			fmt.Fprintf(&body, `<hx-partial hx-target="%s" hx-swap="delete"></hx-partial>`, attr)
		case rec.status() != http.StatusOK:
			slog.WarnContext(r.Context(), "live: target skipped", "url", r.URL.String(), "target", id, "status", rec.status())
		case rootID(rec.body.Bytes()) != id:
			slog.WarnContext(r.Context(), "live: response root is not the target", "url", r.URL.String(), "target", id)
		default:
			fmt.Fprintf(&body, `<hx-partial hx-target="%s" hx-swap="outerMorph">%s</hx-partial>`, attr, rec.body.Bytes())
		}
	}
	if len(skipped) > 0 {
		w.Header().Set("Live-Skipped", strings.Join(skipped, " "))
	}
	setTrigger(w, r, triggers)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(body.Bytes())
}

// partialRequest is r as an htmx partial request for the element id.
func partialRequest(r *http.Request, id string) *http.Request {
	req := r.Clone(r.Context())
	req.Header.Del(TargetsHeader)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Request-Type", "partial")
	req.Header.Set("HX-Target", "#"+id)
	return req
}

// aborts reports whether one target's response must replace the whole batch:
// it sends the browser elsewhere, or says the caller is not signed in.
func aborts(rec *recorder) bool {
	s := rec.status()
	if s >= 300 && s < 400 || s == http.StatusUnauthorized {
		return true
	}
	for _, h := range []string{"HX-Refresh", "HX-Redirect", "HX-Location"} {
		if rec.header.Get(h) != "" {
			return true
		}
	}
	return false
}

// setTrigger merges the targets' HX-Trigger values into one: a comma list
// when every value is one, otherwise a JSON object in which a bare event name
// has the detail {}. A later target's detail for the same event wins.
func setTrigger(w http.ResponseWriter, r *http.Request, values []string) {
	w.Header().Del("HX-Trigger")
	if len(values) == 0 {
		return
	}
	var names []string
	details := map[string]json.RawMessage{}
	object := false
	for _, v := range values {
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, "{") {
			var m map[string]json.RawMessage
			if err := json.Unmarshal([]byte(v), &m); err != nil {
				slog.WarnContext(r.Context(), "live: unreadable HX-Trigger dropped", "value", v, "err", err)
				continue
			}
			object = true
			for name, d := range m {
				if _, seen := details[name]; !seen {
					names = append(names, name)
				}
				details[name] = d
			}
			continue
		}
		for name := range strings.SplitSeq(v, ",") {
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			if _, seen := details[name]; !seen {
				names = append(names, name)
				details[name] = json.RawMessage("{}")
			}
		}
	}
	if len(names) == 0 {
		return
	}
	if !object {
		w.Header().Set("HX-Trigger", strings.Join(names, ", "))
		return
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, name := range names {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, _ := json.Marshal(name)
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(details[name])
	}
	buf.WriteByte('}')
	w.Header().Set("HX-Trigger", buf.String())
}

// recorder captures one target's response, up to max body bytes.
type recorder struct {
	header http.Header
	code   int
	body   bytes.Buffer
	max    int
	over   bool
}

func (rec *recorder) Header() http.Header { return rec.header }

func (rec *recorder) WriteHeader(code int) {
	if rec.code == 0 {
		rec.code = code
	}
}

func (rec *recorder) Write(b []byte) (int, error) {
	rec.WriteHeader(http.StatusOK)
	if rec.body.Len()+len(b) > rec.max {
		rec.over = true
		return 0, io.ErrShortWrite
	}
	return rec.body.Write(b)
}

func (rec *recorder) status() int {
	if rec.code == 0 {
		return http.StatusOK
	}
	return rec.code
}

// rootID returns the id of the first element in body.
func rootID(body []byte) string {
	z := nethtml.NewTokenizer(bytes.NewReader(body))
	for {
		switch z.Next() {
		case nethtml.ErrorToken:
			return ""
		case nethtml.StartTagToken, nethtml.SelfClosingTagToken:
			for {
				k, v, more := z.TagAttr()
				if string(k) == "id" {
					return string(v)
				}
				if !more {
					return ""
				}
			}
		}
	}
}
