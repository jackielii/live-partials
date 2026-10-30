# Library proposals

Where each piece should live, judged only by what the six cases needed (see findings.md for the
evidence). A pattern earned an abstraction when it recurred in at least two cases and the
extracted call site is clearly simpler.

| Piece | Recurred in | Verdict | Home |
|---|---|---|---|
| Keyed component ids + key read-back | 6 cases, 9 components | earned, **shipped** | **structpages v0.11.0**: `Key`, `KeyTarget`, `KeyOf`, `key`/`keytarget` filters (§1) |
| Several targets in one response | 4 cases (1, 2, 5, 6) | earned as a page middleware, **not** structpages API | **live** (`Middleware`) (§2) |
| Load every requested key in one query | case 1 (three rows), any keyed resync | earned where lists are hot, optional | **live** (`Load`, `Keys`) |
| Innermost-middleware hook | every batch | worth proposing, not blocking | **structpages** (§2) |
| Shell parts refetchable from any page | 1 case (sidebar) | not yet | host route + `data-live-url`; revisit with EOS's bell and rail (§3) |
| Live-element attributes | every case | earned, needs nothing from gsx | **live** (`On`); no gsx directive (§4) |
| Hub (coalescing, audience, SSE) | burst + every case's stream | earned | **live** |
| Dispatcher (match, outer wins, escalate, batch, dirty, resync) | every case | earned | **live** (`js/live.js`) |
| Own-write echo suppression | 1 case | not built | possible `Publish` option later |
| Value frames (hybrid) | 1 case, any presence/badge/count | earned for viewer-independent scalars | **live** (`PublishValues`, `Value`, `ValueText`, `Current`) (§6) |
| Fragment frames (hybrid) | 2 cases (3, 6) | earned as an opt-in per component, falls back to pull | **live** (`Fragment`, `Render`, `End`) (§6) |
| Flush through any writer | the hybrid stream behind a non-Flusher wrapper | worth proposing | **structpages** (§7) |

## 1. structpages keyed ids: shipped in v0.11.0

The stage 2 lab wrapped structpages to get instance ids: `live.ID` (a `kid` filter), `live.Key(r)`
parsing `HX-Target`, and `live.Selector` stripping the key before the stock selector saw it.
Without the wrapper the stock selector fell back to `Page` and served a whole document into the row.

structpages v0.11.0 added, close to the proposal:

```go
func Key(ctx context.Context, v any, key string) (string, error)       // component id + "--" + key escaped to [A-Za-z0-9-], other bytes _xx
func KeyTarget(ctx context.Context, v any, key string) (string, error) // "#" + Key
func KeyOf(t RenderTarget) string                                       // the targeted instance's key, "" when unkeyed
```

```gsx
<tr id={burstPage.Row |> key(b.ID)}>
```

```go
case t.Is(p.Row):
	b, err := s.BurstRow(structpages.KeyOf(t))
```

The stock htmx selectors match a keyed id exactly; `RenderTarget` is unchanged (custom targets
carry a key with an optional `Key() string`); `structpages-lint` v0.1.19 checks the filters and
flags ids built by appending to a generated id. The lab deleted `ids.go` (127 lines), the `kid`
filter and the selector wrapper, and lint is clean. Nothing further to propose here.

## 2. Several targets in one response: a page middleware

Stage 2 had `POST /live`: a JSON list of `{url, target}`, each re-run in-process through the app's
whole handler chain. It worked, but authentication and every middleware ran once per item, and the
endpoint had to validate URLs. The owner's alternative, a structpages batch renderer that resolves
page nodes directly, would have had to re-run or skip middleware.

Stage 3 needs neither. The dispatcher sends an ordinary `GET` of the page with
`X-Live-Targets: id id …`; `live.Middleware`, registered with `WithMiddlewares`, calls the page
handler once per id with that id as `HX-Target` and wraps each body in `<hx-partial>`. It uses
only what any structpages middleware has (the `PageNode`, the request context), so no structpages
API is needed. Measured: the outer chain runs once per batch, server time is back to stage 1's level,
and `live.Load` (which reads the batch's targets from the context) turns N row queries into 1.

**Still worth proposing: an innermost-middleware hook.** `WithMiddlewares` wraps the page's own
`Middlewares()`, so the batch splits *outside* them and a page's middleware (a product licence
check, a per-subtree request arg) runs once per target: `TestMiddlewareRunsTheOuterChainOnce`
counts 3 runs for 3 targets. A mount option that places a middleware directly around the page
handler, inside page middlewares, would let a batch run them once:

```go
structpages.WithHandlerMiddleware(live.Middleware) // innermost: after every page Middlewares()
```

The evidence for it is real but small: in the lab no page has its own middleware, and in EOS the
page middlewares are cheap checks. Raise it with structpages; do not block on it.

**Also small: the mount's selector is not visible to middleware.** `live.Batch.Selector` must
repeat the `WithTargetSelector` given to `Mount`, because `live.Load` needs each listed id's
`RenderTarget`. Exposing it (`structpages.TargetSelectorFor(ctx)`, or a `RenderTarget` provider
that middleware can call) would remove the duplication. One line per app; mention it alongside the
hook.

## 3. Shell parts refetchable from any page: not yet

The sidebar's presence dot is rendered by the layout on every page, so no page's Props knows it.
The lab gives it a home route and `data-live-url`:

```go
shell shellPage `route:"GET /shell Shell"`

func (p shellPage) Props(t structpages.RenderTarget, s *store) (person, error) {
	return s.Person(structpages.KeyOf(t))
}
```

```gsx
<li id={shellPage.DM |> key(pr.ID)} { live.On("lab:presence", pr.ID)... } data-live-url={shellPage{} |> url}>
```

Stage 3 makes this cost visible: a batch is one GET per page URL, so a frame touching a shell part
and a page element costs two requests (case 2: 1 → 2). If EOS's bell, rail badges and presence dots
repeat it, the candidate is still a mount-wide structpages option that lets standalone layout
components be answered by any page (`structpages.WithSharedComponents(ui.BellCount, ui.PresenceDot)`
with a request-args provider for their data). The batch middleware would then serve shell targets
from the page's own GET. Decide after the EOS port, not before.

Hybrid (§6) answers the presence case differently: a value frame needs no route at all, so the
sidebar's dot lost its `shellPage` and `data-live-url`. The proposal stays open only for shell parts
that show more than a value (the bell's list).

## 4. gsx: nothing needed

`{ live.On(topic, key)... }` is an ordinary attribute spread and `key` an ordinary structpages
filter. A gsx directive (`live:on=…`) would save a few characters and tie gsx to one library.

## 5. The standalone `live` library

Earned, and small:

- `live.Hub[V]`: `Publish(ctx, audience, topic, ids...)`, `Subscribe`, `ServeSSE`, `Since`,
  `WriteFrame`/`WriteEvent` for hosts merging durable sources. No persistence.
- `live.Audience[V]` / `live.AudienceFunc[V]`.
- `live.On`.
- `live.Middleware` / `live.Batch`, `live.Load`, `live.Keys`, `live.ErrNotFound`.
- `js/live.js`: `start`, plus `createDispatcher`, `plan`, `outermost`, `dirty` for tests.

It depends on structpages for the middleware type, `KeyOf` and `ID`, and on gsx for `gsx.Attrs`.
- (hybrid) `Hub.PublishValues`, `Hub.Current`, `Hub.Fragment`, `Hub.Render`, `Hub.End`,
  `live.Value`, `live.ValueText`, `live.Component`, `live.Fragment`, `live.WriteResync`; in
  `js/live.js`, `setValues` and the dispatcher's `values` and `frame(topic, ids, fragments)`.

## 6. Value and fragment frames (hybrid)

Both ride the existing stream and frame shape, so a host adopts them per topic and per component, and
everything else stays signal + pull. Neither needs structpages or gsx changes: fragment ids come from
`structpages.Key` with the stream request's context, which carries the page tree when the stream is
a structpages route (`ServeHTTP` page). A stream served from a plain mux route gets no page tree,
every fragment fails to get its id, and the page silently pulls (logged).

Not proposed: a per-page subscription telling the stream which components the tab shows. It would
save the renders for subscribers who are in a topic's audience but on another page (measured in
findings), at the cost of the client describing its composition to the server, which is what this
design avoids. Narrow audiences are the lever instead.

## 7. structpages: flushing through `Unwrap`-only wrappers (optional, not a bug)

structpages buffers a page's response by design, so it can catch an error and render it properly,
and it supports flushing: a handler streams by calling `http.NewResponseController(w).Flush()`.
The hybrid's stream stalled because of the lab, not structpages: the lab's `timedWriter` wrapped the
`ResponseWriter` with only an `Unwrap` method and no `Flush`, and structpages' buffered writer
passes a flush on to the writer below it when that writer is an `http.Flusher`. Giving the wrapper a
`Flush` fixed it, and is the right fix: a middleware writer that sits under a streaming route should
implement `http.Flusher`.

Optional nicety, not needed by the lab: `buffered.FlushError` could call
`http.NewResponseController(w.ResponseWriter).Flush()` instead of asserting `http.Flusher`, so
wrappers that follow Go 1.20's `Unwrap` convention without `Flush` would flush too.
