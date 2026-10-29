# Library proposals

Where each piece should live, judged only by what the six cases needed (see findings.md for the
evidence). A pattern earned an abstraction when it recurred in at least two cases and the
extracted call site is clearly simpler. Nothing here edits structpages or gsx.

| Piece | Recurred in | Verdict | Home |
|---|---|---|---|
| Keyed component ids + `Key` | 6 cases, 9 components | earned | **structpages** (§1); `live.ID`/`Key`/`Selector` until then |
| Batch of component targets in one request | 4 cases (1, 2, 5, 6) | earned as a handler, **not** as a structpages renderer | **live** (`BatchHandler`) (§2) |
| Shell parts refetchable from any page | 1 case (sidebar) | not yet | host route + `data-live-url`; revisit with EOS's bell and rail (§3) |
| Live-element attributes | every case | earned, needs nothing from gsx | **live** (`On`); no gsx directive (§4) |
| Hub (coalescing, audience, SSE) | burst + every case's stream | earned | **live** |
| Dispatcher (match, outer wins, escalate, batch, dirty, resync) | every case | earned | **live** (`js/live.js`) |
| Own-write echo suppression | 1 case | not built | possible `Publish` option later |

## 1. structpages: keyed component ids

**Problem.** A component rendered once per row needs an id per instance, and the request
targeting one instance must still select the component and tell Props which instance. Today
neither side exists, so the lab wraps both.

**Repro** (`live/batch_test.go: TestStockSelectorMissesKeyedTargets`): mount a page with a `Row`
component, request it with `HX-Request: true` and `HX-Target: tr#list-row--a`. Pass 0 of
`matchComponentByTarget` finds no exact id, no suffix rule matches a key *after* the component
id, so the target is unmatched and the page answers with `Page`: a whole document for a row.

**Today's workaround** (in this repo):

```go
// live/ids.go
func ID(ctx context.Context, component any, key string) (string, error) // structpages.ID + "--" + escaped key
func Key(r *http.Request) string                                        // parse it back out of HX-Target
func Selector(next structpages.TargetSelector) structpages.TargetSelector // strip the key before next sees it
```

```toml
kid = "github.com/jackielii/live-partials/live.ID"
```

```gsx
<tr id={seriesPage.Row |> kid(s.ID)}>
```

```go
structpages.WithTargetSelector(live.Selector(structpages.HTMXv4RenderTarget))
…
case t.Is(p.Row):
	row, err := s.OneSeries(live.Key(r))
```

Before any of this (tag `direct`), each keyed component had its own route and page struct:

```go
seriesRow seriesRowPage `route:"GET /series/{seriesId}/row Series row"`

type seriesRowPage struct{}
func (seriesRowPage) Props(r *http.Request, s *store) (series, error) {
	return s.OneSeries(r.PathValue("seriesId"))
}
```

```gsx
component (p seriesRowPage) Page(s series) { <SeriesRow s={s}/> }

<tr id={"series-" + s.ID} data-key={s.ID}
	hx-get={seriesRowPage{} |> url(map[string]any{"seriesId": s.ID})}
	hx-trigger="lab:series[hit(detail, this.dataset.key)] from:#stream" hx-swap="outerMorph">
```

**Proposed API.**

```go
// ID and IDTarget take an optional key: the component's id, a separator that generated ids
// never contain ("--"), and the key escaped to [A-Za-z0-9_-] (other bytes as _XX).
func ID(ctx context.Context, v any, key ...string) (string, error)
func IDTarget(ctx context.Context, v any, key ...string) (string, error)

// RenderTarget gains the key of a keyed target ("" otherwise). Is ignores the key.
type RenderTarget interface {
	Is(component any) bool
	Key() string
}
```

`HTMXRenderTarget` and `HTMXv4RenderTarget` split the key off before matching, so pass 0 still
does the exact match. After:

```gsx
<tr id={seriesPage.Row |> id(s.ID)}>
<button hx-get={seriesPage{} |> url} hx-target={seriesPage.Row |> target(s.ID)}>Reload</button>
```

```go
structpages.WithTargetSelector(structpages.HTMXv4RenderTarget) // unchanged
…
case t.Is(p.Row):
	row, err := s.OneSeries(t.Key())
```

This removes `live.ID`, `live.Key`, `live.Selector` and the `kid` filter, and makes keyed
targets work for ordinary htmx too (a row's own "edit" button), not only for the dispatcher.
`structpages-lint` can check `id(key)` like `id`. Changing `RenderTarget` is a breaking change
for custom selectors; `structpages.TargetKey(t RenderTarget) string` with an optional `Keyed`
interface is the non-breaking alternative.

## 2. A batch of component targets: keep it in `live`, not in structpages

The owner suggested an in-process batch renderer in structpages that knows page nodes, so
`POST /live` needn't re-enter the HTTP stack. The evidence says **not warranted**:

- Re-entry is cheap: three items rendered in 0.15–0.45 ms total server time (findings table).
- Re-entry is what applies authentication, authorisation, licence checks and every middleware.
  A renderer that resolves page nodes directly would have to re-run all of them or skip them;
  skipping them is a security bug waiting for the first page whose Props trusts its middleware.
- The handler is 180 lines with no structpages dependency beyond what any handler has, and works
  for any host router.

What structpages *could* add cheaply is `RenderTarget.Key()` (§1) so the batch items need no
special selector.

## 3. Shell parts refetchable from any page: not yet

The sidebar's presence dot is rendered by the layout on every page, so no page's Props knows it.
The lab gives it a home route and `data-live-url`:

```go
shell shellPage `route:"GET /shell Shell"`

func (p shellPage) Props(r *http.Request, s *store) (person, error) {
	return s.Person(live.Key(r))
}
```

```gsx
<li id={shellPage.DM |> kid(pr.ID)} { live.On("lab:presence", pr.ID)... } data-live-url={shellPage{} |> url}>
```

One case, three lines; no library change. If EOS's bell, rail badges and presence dots repeat
it, the candidate is a mount-wide structpages option that lets standalone layout components be
answered by any page, e.g. `structpages.WithSharedComponents(ui.BellCount, ui.PresenceDot)`
with a request-args provider for their data. Decide after the EOS port, not before.

## 4. gsx: nothing needed

`{ live.On(topic, key)... }` is an ordinary attribute spread and `kid` an ordinary filter. A gsx
directive (`live:on=…`) or attribute bundle syntax would save a few characters and tie gsx to
one library. The keyed id belongs in structpages' `id` filter (§1), which gsx already
supports. No gsx proposal.

## 5. The standalone `live` library

Earned, and small:

- `live.Hub[V]`: `Publish(ctx, audience, topic, ids...)`, `Subscribe`, `ServeSSE`, `Since`,
  `WriteFrame`/`WriteEvent` for hosts merging durable sources. No persistence.
- `live.Audience[V]` / `live.AudienceFunc[V]`.
- `live.On`, and until §1 lands `live.ID`, `live.Target`, `live.Key`, `live.Selector`.
- `live.BatchHandler` / `live.Batch`.
- `js/live.js`: `start`, plus `createDispatcher`, `plan`, `outermost`, `dirty` for tests.

It depends on structpages only for `ID` and the selector type; once §1 lands it can drop the
`ids.go` half and depend on gsx only for `gsx.Attrs`.
