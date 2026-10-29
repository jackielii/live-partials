package live

import (
	"context"
	"net/http"
	"strings"

	"github.com/gsxhq/gsx"
	"github.com/jackielii/structpages"
)

// On marks an element live: the dispatcher refetches it when a frame of
// topic arrives. With a key, only frames naming that key (or naming nothing)
// match. Spread it on the element: <tr { live.On("orbit:data", row.ID)... }>.
func On(topic string, key ...string) gsx.Attrs {
	a := gsx.Attrs{{Key: "data-live", Value: topic}}
	if len(key) > 0 {
		a = append(a, gsx.Attr{Key: "data-live-key", Value: key[0]})
	}
	return a
}

// keySep joins a component id and an instance key. structpages ids are
// kebab-case and never contain it, so the first occurrence splits them.
const keySep = "--"

// ID is the element id of one keyed instance of a page component: the
// component's structpages id, keySep and the escaped key. Register it as a
// gsx filter (`kid = "github.com/jackielii/live-partials/live.ID"`) and write
// id={ Page.Row |> kid(row.ID) }.
func ID(ctx context.Context, component any, key string) (string, error) {
	id, err := structpages.ID(ctx, component)
	if err != nil {
		return "", err
	}
	return id + keySep + escapeKey(key), nil
}

// Target is ID as a CSS selector, for hx-target.
func Target(ctx context.Context, component any, key string) (string, error) {
	id, err := ID(ctx, component, key)
	return "#" + id, err
}

// Key returns the instance key of the request's HX-Target, or "" when the
// target is not a keyed id.
func Key(r *http.Request) string {
	_, key, _ := splitTarget(r.Header.Get("HX-Target"))
	return key
}

// Selector wraps a structpages TargetSelector so a keyed HX-Target
// ("tr#series-row--s3") selects its component ("series-row") and Props reads
// the key with Key.
func Selector(next structpages.TargetSelector) structpages.TargetSelector {
	return func(r *http.Request, pn *structpages.PageNode) (structpages.RenderTarget, error) {
		if unkeyed, _, ok := splitTarget(r.Header.Get("HX-Target")); ok {
			r2 := *r
			r2.Header = r.Header.Clone()
			r2.Header.Set("HX-Target", unkeyed)
			r = &r2
		}
		return next(r, pn)
	}
}

// splitTarget splits an htmx 4 HX-Target ("tag#id") whose id is keyed into
// the unkeyed target and the unescaped key.
func splitTarget(target string) (unkeyed, key string, ok bool) {
	tag, id, found := strings.Cut(target, "#")
	if !found {
		return target, "", false
	}
	comp, esc, keyed := strings.Cut(id, keySep)
	if !keyed {
		return target, "", false
	}
	return tag + "#" + comp, unescapeKey(esc), true
}

// escapeKey keeps [A-Za-z0-9-] and writes any other byte as _XX, so the id
// needs no CSS escaping and the key survives the round trip.
func escapeKey(k string) string {
	const hex = "0123456789abcdef"
	var b strings.Builder
	for i := 0; i < len(k); i++ {
		c := k[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('_')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}

func unescapeKey(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '_' && i+2 < len(s) {
			if v, ok := unhex(s[i+1], s[i+2]); ok {
				b.WriteByte(v)
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func unhex(a, b byte) (byte, bool) {
	val := func(c byte) (byte, bool) {
		switch {
		case c >= '0' && c <= '9':
			return c - '0', true
		case c >= 'a' && c <= 'f':
			return c - 'a' + 10, true
		}
		return 0, false
	}
	x, ok1 := val(a)
	y, ok2 := val(b)
	return x<<4 | y, ok1 && ok2
}
