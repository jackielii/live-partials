# live-partials

Server-pushed, precise live updates for Go + [structpages](https://github.com/jackielii/structpages) +
[gsx](https://github.com/gsxhq/gsx) + htmx 4, built evidence first. The repository is the
rationale: six real screens written the naive way, measured, then only the abstractions that paid for
themselves, then the library support that made most of them unnecessary. Each stage is a tag.

| Tag | Stage | What a live component costs |
|---|---|---|
| `direct` | 1. naive: per-element `hx-trigger`, a route per fragment | a route, a page struct, a hand-built id, a filtered trigger |
| `extracted-v1` | 2. extracted: `live.On`, a dispatcher, own keyed ids, `POST /live` | an attribute, an id filter, a Props branch |
| `structpages-keys` | 3. library: structpages keys, a page middleware for batching | the same, with stock structpages filters and no endpoint |
| `datastar` | comparison: the same cases on Datastar 1.0.4, server-pushed HTML and signals | an id, plus a branch in the page's stream push for every component a topic touches |

## The problem

EOS (one Go monolith, server-rendered) needs pages that update themselves when another user changes
data. The server sends a **frame** per change on one SSE stream per tab: a topic and at most opaque
ids (`orbit:data {"ids":["s3"]}`), never content. The page must re-render exactly what shows those
ids. Six cases, each taken from a real EOS screen (realtime design §7):

| # | Case | EOS screen | What must happen |
|---|---|---|---|
| 1 | keyed list + aggregate | Orbit series catalog | one row and the summary tile update, nothing else |
| 2 | one entity, many places | Collaboration presence: sidebar DM, directory row, person card | all three update |
| 3 | editor with unsaved input | ACOSS/FOLIO report draft | a dirty row warns instead of being overwritten; a neighbour updates without moving the caret |
| 4 | insert and delete | Files browse | an upload appears in order, a deletion disappears |
| 5 | burst | Orbit import writing 50 series in 100 ms | every change lands without 50 requests |
| 6 | nested levels | ACOSS operations job panel | a progress tick updates the bar; a status change updates the card once |
| 7 | fan-out | any shared screen | one change seen by 50 viewers: what the server and the wire pay (Go test) |
| 8 | URL state | Orbit catalog filters | a filtered list whose filter is in the URL survives Back, Forward and reload, and stays live |

## Stage 1: direct (`git checkout direct`)

Every live element listens for itself and fetches itself from a route that exists only to render it:

```gsx
<tr id={"series-" + s.ID} data-key={s.ID}
	hx-get={seriesRowPage{} |> url(map[string]any{"seriesId": s.ID})}
	hx-trigger="lab:series[hit(detail, this.dataset.key)] from:#stream" hx-swap="outerMorph">
```

```go
seriesRow seriesRowPage `route:"GET /series/{seriesId}/row Series row"`

func (seriesRowPage) Props(r *http.Request, s *store) (series, error) {
	return s.OneSeries(r.PathValue("seriesId"))
}
```

It works, and every case passes its e2e. Measured on one tab: one request **per element per frame**
(case 2: 3, case 6: 2 with the step line swapped twice, case 5: **50** requests queued behind HTTP/1.1's
six-connection limit, one held by the stream). Five of 14 routes existed only to render one
fragment. Each keyed element repeated the same five things: a hand-built id, `data-key`, an `hx-get` to
its own route, a filtered trigger, a swap. Regions repeated `resync from:#stream`; rows forgot it, so a
missed row frame stayed stale. The editor case needed a dirty check and a side-effecting trigger
filter.

## Stage 2: extracted (`git checkout extracted-v1`)

What recurred in at least two cases, and became clearly simpler once extracted:

- **`live.On(topic, key)`**: the element declares what it shows; no trigger, no route.
- **a dispatcher** (`js/live.js`): match keyed elements, outer wins, escalate to the region past a
  threshold, unknown ids to the insert region, spare dirty editors, resync, and **batch** every
  refetch of an animation frame into one request.
- **a hub** (`live.Hub`): per-subscriber coalescing, so a burst becomes two frames.
- **keyed ids answered by the page's own route.** structpages had no instance ids, so the lab wrapped
  them: `live.ID` (a `kid` filter), `live.Key(r)`, and `live.Selector` rewriting `HX-Target` before
  the stock selector saw it. Without the wrapper, stock structpages served the whole page into the row.
- **`POST /live`**: a batch endpoint re-entering the app's full handler chain once per item.

Requests dropped to one per frame (case 5: 50 → 2), routes from 14 to 10. The rest stayed
case-specific (docs/findings.md). Two parts were workarounds for missing library support: the keyed
ids, and an endpoint that re-ran authentication per item.

## Stage 3: library support (`structpages-keys`, `main`)

**structpages v0.11.0 ships keyed ids**: `Key(ctx, v, key)` / `KeyTarget` (the `key` / `keytarget`
filters) and `KeyOf(t RenderTarget)`. The stock htmx selectors match a keyed `HX-Target` to its
component, so `live.ID`, `live.Target`, `live.Key`, `live.Selector` and the `kid` filter are gone.

**Batching is a middleware, not structpages API.** The dispatcher sends one ordinary `GET` of the page
URL listing the ids in `X-Live-Targets`; `live.Middleware` (registered with `WithMiddlewares`) calls the
page handler once per id with that id as `HX-Target`, and wraps each body in `<hx-partial>`.
Authentication and every outer middleware run once per batch; Props still sees one target and one
key; no endpoint, no URL validation, no stack re-entry. A structpages batch renderer would have to
re-run or skip middleware, and skipping it is a security bug waiting for the first page whose Props
trusts its middleware. The middleware needs only what any middleware has.

**Optional batched loading.** The middleware also puts the batch's targets in the context, so a hot
list can load every requested key in one query: `live.Load` loads them on the first Props call and
serves the rest of the batch from that. Measured on case 1 with three rows changed in one frame: **2
store queries instead of 4** (rows + summary), `examples/lab/lab_test.go`.

**No gsx change.** `{ live.On(…)... }` is an ordinary attribute spread and `key` an ordinary filter.

## Comparison: Datastar (`git checkout datastar`)

Not a stage of the design above: evidence for a comparison. `examples/datastar` writes the same
cases (sharing `examples/labdata`'s store and simulation, and `live.Hub`) the idiomatic Datastar
way: each page opens one stream (`data-init="@get('<page>/updates')"`), and for every hub frame
the page's stream renders what that page shows for its viewer and pushes
`datastar-patch-elements` (morphed by structpages `key` ids) or, for the editor,
`datastar-patch-signals`. No request is made for a live change. Cases 7 (fan-out, a Go test in both
stages) and 8 (URL state, in both) were added to stress the differences.

The client is Datastar **1.0.4**, the latest release (2026-09-21). Its 1.x bundles are published
only in the GitHub repository (npm's `latest` is still `1.0.0-beta.11`), so
`examples/datastar/web/vendor/datastar-1.0.4.js` is `bundles/datastar.js` from tag v1.0.4 with its
source-map comment removed, MIT licence beside it; to upgrade, replace the file and its name. The
server uses `github.com/starfederation/datastar-go` v1.2.2. No Datastar Pro feature is used: the
ones case 8 wanted (`data-query-string`, `data-replace-url`) are Pro, whose licence excludes public
and open-source repositories.

Headline (docs/findings.md, "Datastar vs htmx + live.js"): Datastar makes no request per change,
so latency and server CPU on fan-out are about an order of magnitude lower (50 viewers: 11 against
80 CPU ms, 1.4 against 13 ms until the last tab is updated); stage 3 needs less code per component
(308 against 460 lines for the cases), gets dirty-editor protection and URL state generically, and has safer
reconnect and authorisation defaults.

## Final per-component cost

A keyed live component is one id, one attribute and one Props branch (case 5, exactly as in the lab):

```gsx
<tr id={burstPage.Row |> key(b.ID)} { live.On("lab:burst", b.ID)... }>…</tr>
```

```go
if t.Is(p.Row) {
	b, err := s.BurstRow(structpages.KeyOf(t)) // a not-found error becomes a 404: the row is removed
	return nil, b, err
}
```

or, for a list where one frame often changes several rows (case 1):

```go
case t.Is(p.Row):
	row, err := live.Load(r.Context(), t, p.Row, s.SeriesByID) // SeriesByID(ids []string) (map[string]series, error)
	return seriesProps{}, nil, row, seriesSummary{}, err
```

plus a `hub.Publish(ctx, audience, "lab:burst", id)` where the data changes. A region is the same
without a key (`live.On("lab:burst")` on the `<tbody>`). The keys are client input: Props (or the
loader) must return only what the caller may see.

## Protocol

**Attributes** (read by `js/live.js`):

| Attribute | Meaning |
|---|---|
| `data-live="t1 t2"` | topics the element shows (`live.On`) |
| `data-live-key="k"` | the instance key; without it the element matches every frame of its topic |
| `data-live-url` | URL to refetch from when the current page does not render the element (shell parts) |
| `data-live-max="8"` | on a region: escalate past this many matching keyed elements |
| `data-live-insert` | on a region: refetch it for ids that match no element (new rows) |
| `data-live-stale` | the notice revealed when a change hits an element with unsaved input |
| `data-live-refresh` | a button inside the element that discards the input and refetches |

A *region* is an unkeyed live element containing keyed live elements of the same topic. It is
refetched for an id-less frame, an escalation, an unknown id (with `data-live-insert`) or a resync,
never for an ordinary keyed change.

**Frame** (SSE, no event id so it never moves `Last-Event-ID`):

```text
event: lab:series
data: {"ids":["s2","s5","s8"]}
```

`data: {}` means the whole topic; `event: resync` means refetch every outermost live element. The
stream URL carries `?since=<hub.Since()>` from the render, so the connect-time resync is skipped when
nothing was published in between.

**Request**: one per page URL per animation frame (usually one in all; a shell part's
`data-live-url` adds its own):

```http
GET /series HTTP/1.1
HX-Request: true
X-Live-Targets: series-row--s2 series-row--s5 series-row--s8 series-summary
```

**Response**: each target rendered as an ordinary htmx partial request (`HX-Target: #<id>`), in
request order:

```html
<hx-partial hx-target="#series-row--s2" hx-swap="outerMorph"><tr id="series-row--s2">…</tr></hx-partial>
<hx-partial hx-target="#series-row--s9" hx-swap="delete"></hx-partial>
```

- 200 whose root element has the target's id: morphed in. 404: the element is removed.
- Any other failure skips that target only (logged).
- A redirect, 401, `HX-Refresh`, `HX-Redirect` or `HX-Location` from any target replaces the whole
  response, so the host's usual htmx handling applies.
- `HX-Trigger` from every target is merged into the response on every path.
- Over 64 ids is a 400. Targets past 1 MiB are named in `Live-Skipped`, and the dispatcher queues
  them again.

## Use in a host app

```go
hub := live.NewHub[*User](live.Config{})
mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) { hub.ServeSSE(w, r, currentUser(r)) })

structpages.Mount(mux, pages{}, "/", "App",
	structpages.WithTargetSelector(structpages.HTMXv4RenderTarget),
	structpages.WithMiddlewares(auth, live.Middleware), // live.Middleware innermost of the globals
	structpages.WithArgs(hub), …)

hub.Publish(ctx, canSeeSeries, "orbit:data", series.ID) // after the write commits
```

The host's error handler maps its not-found errors and `live.ErrNotFound` to 404. `canSeeSeries` is
any `live.Audience[*User]`, which gets every subscriber's viewer at once. A host with durable events
(messages, notifications) writes its own SSE handler: replay its outbox after `Last-Event-ID` with
`live.WriteEvent`, then `hub.Subscribe(viewer)` and select over its feed and `Subscription.Ready()`.

`gsx.toml` registers the structpages filters (`url`, `id`, `target`, `key`, `keytarget`). In the page:

```gsx
<div id="stream" hx-sse:connect=f`/events?since=@{hub.Since()}` hidden></div>
```

```js
import "htmx.org/dist/ext/hx-sse.js";
import { start } from "live-partials/js/live.js";
start({ stream: "#stream" });
```

## Run each stage

```sh
git checkout direct          # or extracted-v1, structpages-keys, datastar
npm install
npm run dev                  # http://localhost:5173: the cases, a "simulate other users" panel, a meter
npm run dev:datastar         # (datastar tag) http://localhost:5174: the Datastar app
npm run e2e                  # Playwright, including measure.spec.ts; from datastar on, both apps
```

From the `datastar` tag Playwright has two projects, `lab` (port 8899) and `datastar` (8898);
`npx playwright test -c e2e --project=datastar` runs only the Datastar app. The Datastar meter
counts the same things from the stream itself: frames are stream events, bytes include them, and
server ms and queries arrive in a `lab-meter` event after each push. Each simulate panel has
"Restart the server's streams" (`/sim/drop-streams`), which ends every stream and refuses new ones
for a second.

The meter counts frames, requests, response bytes, swaps, server and swap time for the tab (stage 3
adds store queries). [docs/findings.md](docs/findings.md) has the numbers for all three stages;
[docs/library-proposals.md](docs/library-proposals.md) says what belongs in structpages, gsx or here.

## Test

```sh
go test ./...        # hub, middleware (per-target failure, aborts, triggers, query counting), lab,
                     # Datastar streams, and case 7 (FANOUT_N=200 for more subscribers)
npm run test:js      # dispatcher rules (node --test + jsdom)
npm run e2e          # every case in both apps, two tabs, two viewers, caret, reconnect, costs, measurements
go tool structpages-lint ./...
```
