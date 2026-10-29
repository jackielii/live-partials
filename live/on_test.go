package live

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/gsxhq/gsx"
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
