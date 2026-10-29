package live

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"

	nethtml "golang.org/x/net/html"
)

// Batch serves the dispatcher's POST; BatchHandler is a Batch with the
// default bounds. Zero fields take the defaults in brackets.
type Batch struct {
	Next        http.Handler // the app, with its middleware, that serves each item
	MaxItems    int          // items accepted per request; more is a 400 [64]
	Concurrency int          // items rendered at once [4]
	MaxBytes    int          // total response body; items past it are skipped and named in Live-Skipped [1 MiB]
}

// item is one element to refetch: the page URL it was rendered by and its
// htmx identifier ("tr#series-row--s3"), sent as HX-Target.
type item struct {
	URL    string `json:"url"`
	Target string `json:"target"`
}

// BatchHandler serves the dispatcher's POST: a form field "live" holding a
// JSON list of {url, target}. Each item runs as an in-process GET through
// next with the caller's headers (so cookies and auth apply) plus HX-Request
// and HX-Target. A 200 body is returned inside
// <hx-partial hx-target="#id" hx-swap="outerMorph">; a 404 becomes a partial
// that deletes the element; any other status, or a body whose root element
// is not the target, is skipped and logged.
func BatchHandler(next http.Handler) http.Handler {
	return &Batch{Next: next}
}

func (b *Batch) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	opts := *b
	if opts.MaxItems == 0 {
		opts.MaxItems = 64
	}
	if opts.Concurrency == 0 {
		opts.Concurrency = 4
	}
	if opts.MaxBytes == 0 {
		opts.MaxBytes = 1 << 20
	}
	var items []item
	if err := json.Unmarshal([]byte(r.FormValue("live")), &items); err != nil || len(items) == 0 {
		http.Error(w, "live: want a JSON list of {url, target} in field live", http.StatusBadRequest)
		return
	}
	if len(items) > opts.MaxItems {
		http.Error(w, fmt.Sprintf("live: at most %d items", opts.MaxItems), http.StatusBadRequest)
		return
	}

	parts := make([][]byte, len(items))
	sem := make(chan struct{}, opts.Concurrency)
	var wg sync.WaitGroup
	for i, it := range items {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			parts[i] = render(opts.Next, r, it, opts.MaxBytes)
		})
	}
	wg.Wait()

	var body bytes.Buffer
	var skipped []string
	for i, p := range parts {
		if body.Len()+len(p) > opts.MaxBytes {
			skipped = append(skipped, items[i].Target)
			continue
		}
		body.Write(p)
	}
	if len(skipped) > 0 {
		w.Header().Set("Live-Skipped", strings.Join(skipped, " "))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(body.Bytes())
}

// render runs one item and returns its partial, or nil when it is skipped.
func render(next http.Handler, r *http.Request, it item, maxBytes int) []byte {
	_, id, _ := strings.Cut(it.Target, "#")
	u, err := url.Parse(it.URL)
	if id == "" || err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/") {
		slog.WarnContext(r.Context(), "live: bad batch item", "url", it.URL, "target", it.Target)
		return nil
	}

	req := r.Clone(r.Context())
	req.Method = http.MethodGet
	req.URL = u
	req.RequestURI = u.RequestURI()
	req.Body = http.NoBody
	req.ContentLength = 0
	req.Form, req.PostForm = nil, nil
	req.Header.Del("Content-Type")
	req.Header.Del("Content-Length")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Request-Type", "partial")
	req.Header.Set("HX-Target", it.Target)
	req.Header.Set("HX-Current-URL", it.URL)

	rec := &recorder{header: http.Header{}, max: maxBytes}
	next.ServeHTTP(rec, req)

	attr := html.EscapeString("#" + id)
	switch {
	case rec.status() == http.StatusNotFound:
		return fmt.Appendf(nil, `<hx-partial hx-target="%s" hx-swap="delete"></hx-partial>`, attr)
	case rec.status() != http.StatusOK || rec.over:
		slog.WarnContext(r.Context(), "live: item skipped", "url", it.URL, "target", it.Target, "status", rec.status(), "truncated", rec.over)
		return nil
	case rootID(rec.body.Bytes()) != id:
		slog.WarnContext(r.Context(), "live: response root is not the target", "url", it.URL, "target", it.Target)
		return nil
	}
	return fmt.Appendf(nil, `<hx-partial hx-target="%s" hx-swap="outerMorph">%s</hx-partial>`, attr, rec.body.Bytes())
}

// recorder captures one in-process response, up to max body bytes.
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
