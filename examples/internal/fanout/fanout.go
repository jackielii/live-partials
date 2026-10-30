// Package fanout measures case 7: one change seen by many subscribers. Each
// stage's test opens the subscribers' streams against its real handler,
// makes one change, and reports what every subscriber cost together.
package fanout

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
)

// Result is one stage's cost of one change seen by Subscribers tabs.
type Result struct {
	Stage       string  `json:"stage"`
	Subscribers int     `json:"subscribers"`
	Requests    int     `json:"requests"`  // HTTP requests besides the streams
	Bytes       int64   `json:"bytes"`     // stream bytes after the change plus response bodies
	Brotli      int64   `json:"brotli"`    // the same bytes as compressed on the wire (see the stage's test)
	Queries     int64   `json:"queries"`   // store queries made for the change
	Renders     int     `json:"renders"`   // elements rendered for the change
	ServerMS    float64 `json:"server_ms"` // render time summed over subscribers
	CPUMS       float64 `json:"cpu_ms"`    // process CPU (server and clients) from the change until every tab is updated
	P50MS       float64 `json:"p50_ms"`    // change → subscriber updated, median
	MaxMS       float64 `json:"max_ms"`    // change → last subscriber updated
	PerTabBytes int64   `json:"per_tab_bytes"`
}

// Event is one SSE event.
type Event struct {
	Name, Data string
	Size       int // the event's bytes as decoded, blank line included
}

// Reader reads SSE events one at a time.
type Reader struct{ sc *bufio.Scanner }

func NewReader(r io.Reader) *Reader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	return &Reader{sc}
}

// Next returns the next event; comments are skipped.
func (r *Reader) Next() (Event, error) {
	var ev Event
	var data []string
	for r.sc.Scan() {
		line := r.sc.Text()
		switch {
		case line == "":
			if ev.Name != "" || len(data) > 0 {
				ev.Data = strings.Join(data, "\n")
				ev.Size++
				return ev, nil
			}
			continue
		case strings.HasPrefix(line, ":"):
			continue
		case strings.HasPrefix(line, "event: "):
			ev.Name = line[len("event: "):]
		case strings.HasPrefix(line, "data: "):
			data = append(data, line[len("data: "):])
		}
		ev.Size += len(line) + 1
	}
	if err := r.sc.Err(); err != nil {
		return ev, err
	}
	return ev, io.EOF
}

// BrotliSize is len(b) compressed on its own at the default level.
func BrotliSize(b []byte) int64 {
	var out bytes.Buffer
	w := brotli.NewWriterLevel(&out, brotli.DefaultCompression)
	w.Write(b)
	w.Close()
	return int64(out.Len())
}

// CountingReader counts the bytes read through it.
type CountingReader struct {
	R io.Reader
	N int64
}

func (c *CountingReader) Read(p []byte) (int, error) {
	n, err := c.R.Read(p)
	c.N += int64(n)
	return n, err
}

// CPU is the process's user and system CPU time so far.
func CPU() time.Duration {
	var u syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &u)
	return time.Duration(u.Utime.Nano() + u.Stime.Nano())
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// Sample is what one subscriber spent on one change.
type Sample struct {
	Requests      int
	Bytes, Brotli int64
	Queries       int
	Renders       int
	ServerMS      float64
	Updated       time.Time
}

// Subscribe opens subscriber i's stream and returns once it is subscribed.
// await blocks until the subscriber has handled the next change.
type Subscribe func(i int) (await func() (Sample, error), err error)

// Measure opens n subscribers, then makes rounds changes, one at a time with
// a pause longer than the hub's coalescing gap, and reports the median
// round. CPU is the whole process (server and the simulated tabs) from the
// change until every tab is updated.
func Measure(t *testing.T, stage string, n, rounds int, subscribe Subscribe, change func() error) Result {
	t.Helper()
	awaits := make([]func() (Sample, error), n)
	var wg sync.WaitGroup
	for i := range awaits {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			if awaits[i], err = subscribe(i); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if t.Failed() {
		t.FailNow()
	}
	var results []Result
	for range rounds {
		samples := make([]Sample, n)
		for i := range awaits {
			wg.Add(1)
			go func() {
				defer wg.Done()
				var err error
				if samples[i], err = awaits[i](); err != nil {
					t.Error(err)
				}
			}()
		}
		time.Sleep(20 * time.Millisecond)
		cpu := CPU()
		start := time.Now()
		if err := change(); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		r := Result{Stage: stage, Subscribers: n, CPUMS: ms(CPU() - cpu)}
		lat := make([]time.Duration, n)
		for i, s := range samples {
			r.Requests += s.Requests
			r.Bytes += s.Bytes
			r.Brotli += s.Brotli
			r.Queries += int64(s.Queries)
			r.Renders += s.Renders
			r.ServerMS += s.ServerMS
			lat[i] = s.Updated.Sub(start)
		}
		slices.Sort(lat)
		r.P50MS, r.MaxMS = ms(lat[n/2]), ms(lat[n-1])
		results = append(results, r)
		time.Sleep(300 * time.Millisecond)
	}
	return median(results)
}

// median takes each field's median over the rounds; the counts are the
// same in every round.
func median(rs []Result) Result {
	pick := func(f func(Result) float64) float64 {
		v := make([]float64, len(rs))
		for i, r := range rs {
			v[i] = f(r)
		}
		slices.Sort(v)
		return v[len(v)/2]
	}
	out := rs[0]
	out.ServerMS = pick(func(r Result) float64 { return r.ServerMS })
	out.CPUMS = pick(func(r Result) float64 { return r.CPUMS })
	out.P50MS = pick(func(r Result) float64 { return r.P50MS })
	out.MaxMS = pick(func(r Result) float64 { return r.MaxMS })
	out.ServerMS = float64(int(out.ServerMS*100)) / 100
	return out
}

// N is the subscriber count: FANOUT_N, or 50.
func N() int {
	if n, err := strconv.Atoi(os.Getenv("FANOUT_N")); err == nil && n > 0 {
		return n
	}
	return 50
}

// Report logs r and writes it to test-results/fanout-<stage>.json at the
// repository root.
func Report(t *testing.T, r Result) {
	t.Helper()
	r.PerTabBytes = r.Bytes / int64(r.Subscribers)
	b, _ := json.MarshalIndent(r, "", "  ")
	t.Logf("%s", b)
	dir := filepath.Join("..", "..", "test-results")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fanout-"+r.Stage+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

var sinceRe = regexp.MustCompile(`since=([^"&]+)`)

// Since reads the hub position the page at u renders into its stream URL.
func Since(t *testing.T, c *http.Client, u string) string {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	m := sinceRe.FindSubmatch(b)
	if m == nil {
		t.Fatalf("no since in %s", u)
	}
	s, err := url.QueryUnescape(string(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// ServerTiming is the app;dur= of r's Server-Timing, in milliseconds.
func ServerTiming(r *http.Response) float64 {
	_, v, _ := strings.Cut(r.Header.Get("Server-Timing"), "dur=")
	f, _ := strconv.ParseFloat(v, 64)
	return f
}
