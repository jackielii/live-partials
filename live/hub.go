// Package live pushes "this changed" signals to browser tabs and lets the
// page refetch exactly the elements showing what changed.
//
// A Hub fans topic frames out to SSE subscribers. Frames carry a topic and,
// at most, opaque ids; never content. The page's dispatcher (js/live.js)
// matches frames to elements marked with On and refetches them in one POST
// to a BatchHandler.
package live

import (
	"context"
	"slices"
	"sync"
	"time"
)

// Frame is one signal: a topic and the ids it concerns. Nil IDs means the
// whole topic changed.
type Frame struct {
	Topic string
	IDs   []string
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
	return c
}

// Hub fans frames out to the subscribers whose viewer the frame's audience
// admits. V is the host's viewer type (a user, a session). A Hub keeps
// nothing: a frame published while a tab is disconnected is gone, and the
// tab's resync on reconnect covers it.
type Hub[V any] struct {
	cfg  Config
	mu   sync.Mutex
	subs map[*Subscription]V
}

func NewHub[V any](cfg Config) *Hub[V] {
	return &Hub[V]{cfg: cfg.withDefaults(), subs: map[*Subscription]V{}}
}

// Publish sends topic with ids to every subscriber aud admits. With no ids
// the frame concerns the whole topic.
func (h *Hub[V]) Publish(ctx context.Context, aud Audience[V], topic string, ids ...string) {
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
			s.signal(topic, ids)
		}
	}
}

// Subscribe registers viewer until the Subscription is closed.
func (h *Hub[V]) Subscribe(viewer V) *Subscription {
	s := &Subscription{cfg: h.cfg, ready: make(chan struct{}, 1), topics: map[string]*topicState{}}
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

func (s *Subscription) signal(topic string, ids []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	t := s.topics[topic]
	if t == nil {
		t = &topicState{}
		s.topics[topic] = t
	}
	if t.held != nil {
		if len(t.held.IDs) == 0 || len(ids) == 0 {
			t.held.IDs = nil
		} else {
			t.held.IDs = s.union(t.held.IDs, ids)
		}
		return
	}
	now := time.Now()
	if wait := t.lastSent.Add(s.cfg.Gap).Sub(now); wait > 0 {
		t.held = &Frame{Topic: topic, IDs: s.union(nil, ids)}
		t.timer = time.AfterFunc(wait, func() { s.release(t) })
		return
	}
	t.lastSent = now
	s.enqueue(Frame{Topic: topic, IDs: s.union(nil, ids)})
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
