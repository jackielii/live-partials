package live

import (
	"context"
	"errors"
	"slices"

	"github.com/jackielii/structpages"
)

// ErrNotFound is returned by Load for a key its loader did not return. The
// host's error handler must answer it with 404, which removes the element.
var ErrNotFound = errors.New("live: not found")

type batchKey struct{}

// batch is what Middleware shares with the Props calls of one request: the
// target of every listed id, and what Load has loaded for them so far. The
// targets are served one at a time, so it needs no lock.
type batch struct {
	targets []structpages.RenderTarget
	loaded  map[string]any // component id → loaded[T]
}

type loaded[T any] struct {
	items map[string]T
	err   error
}

// Keys returns the key of every instance of component the current batch
// asks for, in request order, or nil outside a batch.
func Keys(ctx context.Context, component any) []string {
	b, _ := ctx.Value(batchKey{}).(*batch)
	if b == nil {
		return nil
	}
	var keys []string
	for _, t := range b.targets {
		if k := structpages.KeyOf(t); k != "" && t.Is(component) && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	return keys
}

// Load returns the item for the instance of component that t targets. In a
// batch, the first call loads every key the batch asks for with one call to
// load and later calls in the same batch read from that; outside one, load
// gets just t's key. A key load does not return is ErrNotFound.
//
//	case t.Is(p.Row):
//		row, err := live.Load(r.Context(), t, p.Row, s.Rows) // Rows(ids []string) (map[string]row, error)
//
// The keys are client input: load must return only what the caller may see.
func Load[T any](ctx context.Context, t structpages.RenderTarget, component any, load func(keys []string) (map[string]T, error)) (T, error) {
	var zero T
	key := structpages.KeyOf(t)
	keys := Keys(ctx, component)
	if !slices.Contains(keys, key) {
		items, err := load([]string{key})
		return pick(items, err, key)
	}
	b := ctx.Value(batchKey{}).(*batch)
	id, err := structpages.ID(ctx, component)
	if err != nil {
		return zero, err
	}
	l, ok := b.loaded[id].(loaded[T])
	if !ok {
		l.items, l.err = load(keys)
		b.loaded[id] = l
	}
	return pick(l.items, l.err, key)
}

func pick[T any](items map[string]T, err error, key string) (T, error) {
	var zero T
	if err != nil {
		return zero, err
	}
	v, ok := items[key]
	if !ok {
		return zero, ErrNotFound
	}
	return v, nil
}
