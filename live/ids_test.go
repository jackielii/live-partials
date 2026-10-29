package live

import (
	"net/http/httptest"
	"testing"

	"github.com/gsxhq/gsx"
	"github.com/google/go-cmp/cmp"
)

func TestOn(t *testing.T) {
	if got, want := On("orbit:data"), (gsx.Attrs{{Key: "data-live", Value: "orbit:data"}}); !cmp.Equal(got, want) {
		t.Errorf("unkeyed: %v", got)
	}
	want := gsx.Attrs{{Key: "data-live", Value: "orbit:data"}, {Key: "data-live-key", Value: "s1"}}
	if got := On("orbit:data", "s1"); !cmp.Equal(got, want) {
		t.Errorf("keyed: %v", got)
	}
}

func TestKeyEscaping(t *testing.T) {
	for _, k := range []string{"s1", "0c8f-11ee-be56", "a/b c", "x--y", "_5f", "über"} {
		esc := escapeKey(k)
		for _, c := range esc {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				t.Errorf("escapeKey(%q) = %q has %q", k, esc, c)
			}
		}
		if got := unescapeKey(esc); got != k {
			t.Errorf("round trip %q -> %q -> %q", k, esc, got)
		}
	}
}

func TestKey(t *testing.T) {
	for target, want := range map[string]string{
		"tr#series-row--s3":   "s3",
		"tr#series-row--x--y": "x--y",
		"tr#series-row":       "",
		"#series-row--a_2fb":  "a/b",
		"form":                "",
		"":                    "",
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("HX-Target", target)
		if got := Key(r); got != want {
			t.Errorf("Key(%q) = %q, want %q", target, got, want)
		}
	}
}
