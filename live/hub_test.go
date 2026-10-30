package live

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
)

var everyone = AudienceFunc[string](func(string) bool { return true })

func take(s *Subscription) []Frame {
	f, _ := s.Take()
	return f
}

func TestCoalescing(t *testing.T) {
	tests := []struct {
		name  string
		burst [][]string // signals of one topic sent 10ms apart after a first one
		want  []string   // ids of the held frame released after the gap; nil = id-less
	}{
		{"ids merge without duplicates", [][]string{{"b"}, {"c", "b"}, {"d"}}, []string{"b", "c", "d"}},
		{"an id-less signal wins", [][]string{{"b"}, nil, {"c"}}, nil},
		{"more than MaxIDs drops the ids", [][]string{{"1", "2", "3"}, {"4", "5"}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := NewHub[string](Config{MaxIDs: 4})
				s := h.Subscribe("u")
				defer s.Close()
				ctx := context.Background()

				h.Publish(ctx, everyone, "t", "a")
				if got := take(s); !cmp.Equal(got, []Frame{{Topic: "t", IDs: []string{"a"}}}) {
					t.Fatalf("first signal should go at once, got %v", got)
				}
				for _, ids := range tt.burst {
					time.Sleep(10 * time.Millisecond)
					h.Publish(ctx, everyone, "t", ids...)
				}
				if got := take(s); len(got) != 0 {
					t.Fatalf("signals within the gap should be held, got %v", got)
				}
				time.Sleep(250 * time.Millisecond)
				synctest.Wait()
				got := take(s)
				if len(got) != 1 || got[0].Topic != "t" {
					t.Fatalf("want one released frame, got %v", got)
				}
				if !cmp.Equal(got[0].IDs, tt.want) {
					t.Errorf("released ids = %v, want %v", got[0].IDs, tt.want)
				}
			})
		})
	}
}

func TestCoalescingIsPerTopicAndPerSubscriber(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHub[string](Config{})
		a, b := h.Subscribe("a"), h.Subscribe("b")
		defer a.Close()
		defer b.Close()
		ctx := context.Background()
		h.Publish(ctx, everyone, "x", "1")
		h.Publish(ctx, everyone, "y", "1")
		h.Publish(ctx, AudienceFunc[string](func(v string) bool { return v == "b" }), "x", "2")
		if got := take(a); len(got) != 2 {
			t.Errorf("a: each topic's first frame goes at once, got %v", got)
		}
		if got := take(b); len(got) != 2 {
			t.Errorf("b: got %v", got)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if got := take(a); len(got) != 0 {
			t.Errorf("a was not in the audience of the held frame, got %v", got)
		}
		if got := take(b); !cmp.Equal(got, []Frame{{Topic: "x", IDs: []string{"2"}}}) {
			t.Errorf("b: got %v", got)
		}
	})
}

type recordingAudience struct{ seen []string }

func (r *recordingAudience) Admit(_ context.Context, topic string, ids []string, viewers []string) []bool {
	r.seen = slices.Clone(viewers)
	slices.Sort(r.seen)
	out := make([]bool, len(viewers))
	for i, v := range viewers {
		out[i] = strings.HasPrefix(v, "staff")
	}
	return out
}

func TestAudienceSeesEveryViewerOnce(t *testing.T) {
	h := NewHub[string](Config{})
	staff, guest := h.Subscribe("staff-1"), h.Subscribe("guest")
	defer staff.Close()
	defer guest.Close()
	aud := &recordingAudience{}
	h.Publish(context.Background(), aud, "t", "1")
	if !cmp.Equal(aud.seen, []string{"guest", "staff-1"}) {
		t.Errorf("Admit saw %v", aud.seen)
	}
	if len(take(staff)) != 1 || len(take(guest)) != 0 {
		t.Error("only staff should hear the frame")
	}
}

func TestOverflowBecomesResync(t *testing.T) {
	h := NewHub[string](Config{Buffer: 2})
	s := h.Subscribe("u")
	defer s.Close()
	for _, topic := range []string{"a", "b", "c"} {
		h.Publish(context.Background(), everyone, topic)
	}
	frames, resync := s.Take()
	if !resync || len(frames) != 0 {
		t.Errorf("want resync and no frames, got %v %v", frames, resync)
	}
	h.Publish(context.Background(), everyone, "d")
	if frames, resync := s.Take(); resync || len(frames) != 1 {
		t.Errorf("after a resync frames flow again, got %v %v", frames, resync)
	}
}

func TestClosedSubscriptionHearsNothing(t *testing.T) {
	h := NewHub[string](Config{})
	s := h.Subscribe("u")
	s.Close()
	h.Publish(context.Background(), everyone, "t")
	if len(take(s)) != 0 {
		t.Error("closed subscription got a frame")
	}
}

func TestServeSSE(t *testing.T) {
	h := NewHub[string](Config{Heartbeat: 20 * time.Millisecond})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeSSE(w, r, "u")
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	lines := bufio.NewScanner(res.Body)
	next := func() string {
		if !lines.Scan() {
			t.Fatal("stream ended")
		}
		return lines.Text()
	}
	want := []string{": connected", "", "event: resync", "data: {}", ""}
	for _, w := range want {
		if got := next(); got != w {
			t.Fatalf("got %q, want %q", got, w)
		}
	}
	if got := next(); got != ": ping" {
		t.Fatalf("want a heartbeat, got %q", got)
	}
	h.Publish(context.Background(), everyone, "orbit:data", "s1", "s2")
	h.Publish(context.Background(), everyone, "orbit:presence")
	var got []string
	for len(got) < 4 {
		if l := next(); l != "" && l != ": ping" {
			got = append(got, l)
		}
	}
	wantFrames := []string{"event: orbit:data", `data: {"ids":["s1","s2"]}`, "event: orbit:presence", "data: {}"}
	if !cmp.Equal(got, wantFrames) {
		t.Errorf("frames:\n%s", cmp.Diff(wantFrames, got))
	}
}

func TestServeSSESkipsResyncWhenNothingWasPublished(t *testing.T) {
	h := NewHub[string](Config{})
	stream := func(since string) *bufio.Scanner {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.ServeSSE(w, r, "u")
		}))
		t.Cleanup(srv.Close)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"?since="+since, nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { res.Body.Close() })
		return bufio.NewScanner(res.Body)
	}
	rendered := h.Since()
	s := stream(rendered)
	s.Scan()
	s.Scan()
	h.Publish(context.Background(), everyone, "t")
	if s.Scan(); s.Text() != "event: t" {
		t.Fatalf("a current tab should get no resync, got %q", s.Text())
	}
	s = stream(rendered)
	s.Scan()
	s.Scan()
	if s.Scan(); s.Text() != "event: resync" {
		t.Fatalf("a tab rendered before a publish should resync, got %q", s.Text())
	}
}
