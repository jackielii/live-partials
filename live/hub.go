// Package live pushes changes to browser tabs on one SSE stream per tab.
//
// A Hub fans topic frames out to subscribers. A frame is one of three kinds:
//
//   - a signal: a topic and opaque ids. The page's dispatcher (js/live.js)
//     matches it to elements marked with On and refetches them in one GET per
//     page URL, answered by Middleware.
//   - a value frame (PublishValues): a topic and key → value pairs that every
//     recipient may see. The dispatcher writes each value into every element
//     marked with Value or ValueText for that key; nothing is fetched.
//   - a fragment frame: a signal whose topic has components registered with
//     Hub.Fragment. Each subscriber's stream renders them for its own viewer
//     and sends the HTML with the signal; the dispatcher morphs it into the
//     element with that id and refetches only what no fragment covered.
package live

import (
	"context"
	"crypto/rand"
	"maps"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Frame is one signal: a topic and the ids it concerns. Nil IDs means the
// whole topic changed. A value frame has Values instead of IDs.
type Frame struct {
	Topic  string
	IDs    []string
	Values map[string]string
}

// Audience decides which viewers may hear a frame. Admit receives every
// current subscriber's viewer at once, so an implementation can batch its
// permission check, and returns one verdict per viewer.
type Audience[V any] interface {
	Admit(ctx context.Context, topic string, ids []string, viewers []V) []bool
}

// AudienceFunc admits each viewer for which it returns true.
type AudienceFunc[V any] func(viewer V) bool

func (f AudienceFunc[V]) Admit(_ context.Context, _ string, _ []string, viewers []V) []bool {
	out := make([]bool, len(viewers))
	for i, v := range viewers {
		out[i] = f(v)
	}
	return out
}

// Config tunes a Hub. Zero fields take the defaults in brackets.
type Config struct {
	Gap          time.Duration // minimum time between two frames of one topic to one subscriber [250ms]
	MaxIDs       int           // a merged frame with more ids becomes id-less [16]
	Buffer       int           // frames queued per subscriber before it is told to resync [256]
	Heartbeat    time.Duration // SSE comment interval [15s]
	WriteTimeout time.Duration // deadline for each SSE write [10s]
	MaxFragment  int           // bytes of rendered fragments per frame; the rest are pulled [256 KiB]
}

func (c Config) withDefaults() Config {
	if c.Gap == 0 {
		c.Gap = 250 * time.Millisecond
	}
	if c.MaxIDs == 0 {
		c.MaxIDs = 16
	}
	if c.Buffer == 0 {
		c.Buffer = 256
	}
	if c.Heartbeat == 0 {
		c.Heartbeat = 15 * time.Second
	}
	if c.WriteTimeout == 0 {
		c.WriteTimeout = 10 * time.Second
	}
	if c.MaxFragment == 0 {
		c.MaxFragment = 256 << 10
	}
	return c
}

// Hub fans frames out to the subscribers whose viewer the frame's audience
// admits. V is the host's viewer type (a user, a session). A Hub keeps
// nothing: a frame published while a tab is disconnected is gone, and the
// tab's resync on reconnect covers it.
type Hub[V any] struct {
	cfg       Config
	epoch     string
	seq       atomic.Uint64
	mu        sync.Mutex
	subs      map[*Subscription]V
	fragments map[string][]fragment[V]
	current   map[string]CurrentFunc[V]
}

// Since names this hub's position: it changes with every Publish. A page
// renders it into its stream URL (?since=…) so ServeSSE can tell whether
// anything was published between the render and the connection. It is
// unique per Hub, so a tab rendered by another replica always resyncs.
func (h *Hub[V]) Since() string {
	return h.epoch + "." + strconv.FormatUint(h.seq.Load(), 10)
}

func NewHub[V any](cfg Config) *Hub[V] {
	return &Hub[V]{
		cfg:       cfg.withDefaults(),
		epoch:     rand.Text()[:10],
		subs:      map[*Subscription]V{},
		fragments: map[string][]fragment[V]{},
		current:   map[string]CurrentFunc[V]{},
	}
}

// Publish sends topic with ids to every subscriber aud admits. With no ids
// the frame concerns the whole topic.
func (h *Hub[V]) Publish(ctx context.Context, aud Audience[V], topic string, ids ...string) {
	h.publish(ctx, aud, Frame{Topic: topic, IDs: ids}, ids)
}

// PublishValues sends topic's new values to every subscriber aud admits, as
// one value frame. Every admitted viewer receives the same values, so they
// must be what each of them may see; aud is the only check. Values of one
// topic published within the coalescing gap merge, the later value of a key
// winning.
func (h *Hub[V]) PublishValues(ctx context.Context, aud Audience[V], topic string, values map[string]string) {
	if len(values) == 0 {
		return
	}
	h.publish(ctx, aud, Frame{Topic: topic, Values: maps.Clone(values)}, slices.Sorted(maps.Keys(values)))
}

// End closes the stream of every subscriber whose viewer match selects, so
// its tab reconnects through the host's authentication and resyncs. Call it
// when a viewer's permissions change: fragments are rendered and value
// snapshots read as the viewer the stream was opened for.
func (h *Hub[V]) End(match func(V) bool) int {
	h.seq.Add(1) // the reconnect must resync, whatever was published
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for s, v := range h.subs {
		if match(v) {
			s.end()
			n++
		}
	}
	return n
}

func (h *Hub[V]) publish(ctx context.Context, aud Audience[V], f Frame, ids []string) {
	topic := f.Topic
	h.seq.Add(1)
	h.mu.Lock()
	subs := make([]*Subscription, 0, len(h.subs))
	viewers := make([]V, 0, len(h.subs))
	for s, v := range h.subs {
		subs = append(subs, s)
		viewers = append(viewers, v)
	}
	h.mu.Unlock()
	if len(subs) == 0 {
		return
	}
	ok := aud.Admit(ctx, topic, ids, viewers)
	for i, s := range subs {
		if i < len(ok) && ok[i] {
			s.signal(f)
		}
	}
}

// Subscribe registers viewer until the Subscription is closed.
func (h *Hub[V]) Subscribe(viewer V) *Subscription {
	s := &Subscription{cfg: h.cfg, ready: make(chan struct{}, 1), ended: make(chan struct{}), topics: map[string]*topicState{}}
	s.done = func() {
		h.mu.Lock()
		delete(h.subs, s)
		h.mu.Unlock()
	}
	h.mu.Lock()
	h.subs[s] = viewer
	h.mu.Unlock()
	return s
}

// Subscription is one tab's feed. Frames of one topic are coalesced before
// they are queued: the first frame after a quiet gap goes at once, later
// ones within the gap merge into one held frame sent when the gap ends, so
// the last frame of a burst always arrives.
type Subscription struct {
	cfg    Config
	done   func()
	ready  chan struct{}
	ended  chan struct{}
	once   sync.Once
	mu     sync.Mutex
	queue  []Frame
	resync bool
	closed bool
	topics map[string]*topicState
}

type topicState struct {
	lastSent time.Time
	held     *Frame
	timer    *time.Timer
}

// Ready is signalled whenever Take has something to return.
func (s *Subscription) Ready() <-chan struct{} { return s.ready }

// Ended is closed when the hub ends the subscription (Hub.End); the stream
// should then close.
func (s *Subscription) Ended() <-chan struct{} { return s.ended }

func (s *Subscription) end() { s.once.Do(func() { close(s.ended) }) }

// Take returns the queued frames. resync reports that frames were lost
// (the queue overflowed); the listener should then refetch everything, and
// frames is empty.
func (s *Subscription) Take() (frames []Frame, resync bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	frames, resync = s.queue, s.resync
	s.queue, s.resync = nil, false
	return frames, resync
}

// Close unregisters the subscription and stops its pending timers.
func (s *Subscription) Close() {
	s.done()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, t := range s.topics {
		if t.timer != nil {
			t.timer.Stop()
		}
	}
}

func (s *Subscription) signal(f Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	// Value frames coalesce apart from signals of the same topic: merging
	// values into an id-less signal would lose them.
	key := f.Topic
	if f.Values != nil {
		key = "\x00" + f.Topic
	}
	t := s.topics[key]
	if t == nil {
		t = &topicState{}
		s.topics[key] = t
	}
	if t.held != nil {
		switch {
		case f.Values != nil:
			maps.Copy(t.held.Values, f.Values)
		case len(t.held.IDs) == 0 || len(f.IDs) == 0:
			t.held.IDs = nil
		default:
			t.held.IDs = s.union(t.held.IDs, f.IDs)
		}
		return
	}
	fresh := Frame{Topic: f.Topic, IDs: s.union(nil, f.IDs)}
	if f.Values != nil {
		fresh = Frame{Topic: f.Topic, Values: maps.Clone(f.Values)}
	}
	now := time.Now()
	if wait := t.lastSent.Add(s.cfg.Gap).Sub(now); wait > 0 {
		t.held = &fresh
		t.timer = time.AfterFunc(wait, func() { s.release(t) })
		return
	}
	t.lastSent = now
	s.enqueue(fresh)
}

func (s *Subscription) release(t *topicState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || t.held == nil {
		return
	}
	t.lastSent = time.Now()
	s.enqueue(*t.held)
	t.held, t.timer = nil, nil
}

// union adds ids to held without duplicates. More than MaxIDs ids become
// nil: the frame then concerns the whole topic.
func (s *Subscription) union(held, ids []string) []string {
	for _, id := range ids {
		if !slices.Contains(held, id) {
			held = append(held, id)
		}
	}
	if len(held) > s.cfg.MaxIDs {
		return nil
	}
	return held
}

func (s *Subscription) enqueue(f Frame) {
	if !s.resync {
		if len(s.queue) >= s.cfg.Buffer {
			s.queue, s.resync = nil, true
		} else {
			s.queue = append(s.queue, f)
		}
	}
	select {
	case s.ready <- struct{}{}:
	default:
	}
}
