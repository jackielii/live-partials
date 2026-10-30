# Findings

What the lab showed about the EOS realtime design §4.3–4.4
(`eos/docs/superpowers/specs/2026-10-29-eos-realtime-design.md`). The work went evidence first,
in three tagged stages: the six cases written directly (`direct`: per-element `hx-trigger`, a
lab-local broadcaster, no library); the recurring parts extracted into `live/` and `js/live.js`
(`extracted-v1`: own keyed ids, a `POST /live` batch endpoint); then structpages v0.11.0's keyed
ids and a page middleware in place of the endpoint (`structpages-keys`). A fourth, `datastar`, is
not a stage of that design but evidence for a comparison: the same cases written the idiomatic
Datastar way (server-pushed HTML and signals on one stream per page), plus two cases that stress
the differences (7 fan-out, 8 URL state). See "Datastar vs htmx + live.js" at the end.

## Measurements

One tab, Chromium headless on localhost, in-memory store, after the page settled. "Server ms" is
the handler's time to first byte summed over the tab's requests (`Server-Timing`); "swap ms" is
`htmx:before:swap` → `htmx:after:swap`; "queries" counts store calls (each exported store method is
one query), reported per response in `X-Queries`. For Datastar (DS) "requests" excludes the page's
stream, "bytes" are the stream's event bytes plus any response body, "swaps" are elements patched
(a signal patch or a removal counts one), "swap ms" is Datastar applying the patch event, and
"server ms" and "queries" are reported by the stream after each push (render time only: there is
no request to time). The htmx columns count response bodies only: neither the SSE frame (~40 bytes)
nor HTTP headers. Stage 3 and DS are medians of three runs taken in the same session; the counts
were identical in every run. Raw data: `docs/measurements/{direct,extracted-v1,structpages-keys,datastar}.json`;
reproduce with `LAB_VARIANT=… npx playwright test -c e2e measure.spec.ts` at each tag, and
`npx playwright test -c e2e --project=datastar measure` for DS.

Requests / response bytes / swaps per stage (1 `direct` → 2 `extracted-v1` → 3 `structpages-keys` | DS `datastar`):

| Case: action | Requests 1 → 2 → 3 \| DS | Bytes 1 → 2 → 3 \| DS | Swaps 1 → 2 → 3 \| DS | Queries 3 \| DS | Server ms 1 → 2 → 3 \| DS | Swap ms 1 → 2 → 3 \| DS |
|---|---|---|---|---|---|---|
| 1 one of ten series rows changes | 2 → **1** → 1 \| **0** | 668 → 663 → 663 \| 547 | 2 → 2 → 2 \| 2 | 2 \| 2 | 0.07 → 0.27 → 0.13 \| 0.03 | 0.6 → 0.9 → 0.9 \| 0.8 |
| 1 three rows change in one frame | — → — → 1 \| 0 | — → — → 1139 \| 889 | — → — → 4 \| 4 | **2** \| 2 | — → — → 0.14 \| 0.02 | — → — → 1.2 \| 1.0 |
| 2 one person, shown in three places | 3 → **1** → 2 \| 0 | 1056 → 1068 → 1068 \| 837 | 3 → 3 → 3 \| 3 | 3 \| 2 | 0.10 → 0.44 → 0.22 \| 0.02 | 1.0 → 1.0 → 1.1 \| 0.8 |
| 3 neighbour draft row edited | 1 → 1 → 1 \| 0 | 819 → 717 → 717 \| **94** | 1 → 1 → 1 \| 1 | 1 \| 1 | 0.06 → 0.18 → 0.10 \| 0.01 | 0.9 → 0.6 → 0.8 \| 0.5 |
| 4 upload, then delete | 2 → 2 → 2 \| 0 | 1621 → 1436 → 1436 \| 1135 | 2 → 4 → 4 \| 4 | 4 \| 4 | 0.05 → 0.28 → 0.16 \| 0.06 | 0.6 → 1.1 → 1.0 \| 1.1 |
| 5 fifty rows within 100 ms | 50 → **2** → 2 \| 0 | 9514 → 7171 → 7171 \| 4551 | 50 → 2 → 2 \| 2 | 2 \| 2 | 0.76 → 0.28 → 0.21 \| 0.09 | 3.7 → 1.6 → 1.6 \| 1.4 |
| 6 job progress tick | 1 → 1 → 1 \| 0 | 313 → 304 → 304 \| 227 | 1 → 1 → 1 \| 1 | 1 \| 1 | 0.05 → 0.15 → 0.10 \| 0.01 | 0.7 → 0.6 → 0.7 \| 0.6 |
| 6 job status change | 2 → **1** → 1 \| 0 | 1085 → 704 → 704 \| 534 | 2 → **1** → 1 \| 1 | 1 \| 1 | 0.09 → 0.11 → 0.08 \| 0.03 | 0.9 → 0.7 → 0.8 \| 0.8 |
| 8 filter link clicked (`?status=failing`) | — → — → 1 \| 0 + 1 stream | — → — → 4974 \| 1502 | — → — → 1 \| 2 | 2 \| 2 | — → — → 0.10 \| 0.02 | — → — → 3.3 \| 0.3 |
| 8 change inside the filter | — → — → 1 \| 0 | — → — → 557 \| 384 | — → — → 2 \| 1 | 2 \| 1 | — → — → 0.09 \| 0.02 | — → — → 0.8 \| 0.3 |
| resync on connect (series page) | 1 → 0 → 0 \| 0 | 424 → 0 → 0 \| 0 | 1 → 0 → 0 \| 0 | 0 \| 0 | — | — |

DS sends one patch-elements event per element (see "Datastar vs htmx + live.js"), so its stream
events per action are the swaps column: 2, 4, 3, 1 (a signal patch), 4, 2, 1, 1, 2, 1. Case 7 (fan-out)
is measured in Go, below.

Queries were instrumented in stage 3 only. Stage 2's Props were the same per target, so its queries
equal stage 3's except for the "three rows" action, which stage 3 added and which costs one query
per target (4) without `live.Load`: `examples/lab/lab_test.go: TestBatchedLoading` serves the same
four targets both ways and counts 2 against 4.

Before the `?since=` change the stage 2 connect resync cost 1 request, 4070 bytes and 8 swaps on
the series page (every outermost live element, including the six sidebar rows), on every page load.

Reading the numbers:

- **Requests** are where the design pays: one per frame batch, so cases 1, 2 and 6 halve or
  better, and the burst drops from 50 to 2 (the first frame of the burst goes at once, the rest
  coalesce in the hub into one id-less frame that refetches the table).
- **Stage 3 costs case 2 one request.** A batch is one GET *per page URL*, and the sidebar row is
  a shell part answered by `/shell` (`data-live-url`), so the frame becomes `GET /people` (row,
  card) and `GET /shell` (sidebar), sent together. Stage 2's endpoint took items for any URL in
  one POST. Bytes and swaps are unchanged. Every shell part on a page costs this one extra
  request per frame that touches it; see library-proposals §3.
- **Queries** follow targets unless the page opts in: `live.Load` turns N keyed targets of one
  component into one query. Unkeyed regions that read the same data still query separately (case
  4's count and list both read the file list: 2 queries for the upload).
- **Bytes** barely move. The `<hx-partial>` wrapper (~60 bytes per item) offsets what one
  request saves over several. Batching saves round trips and connection slots, not bandwidth.
- **Server time** is sub-millisecond at every stage. Stage 2 was the highest per request because
  `POST /live` re-entered the whole mux and middleware per item; stage 3 runs the outer chain once
  and only the page handler per target, and comes back to within noise of stage 1.
- **Swap time** is dominated by htmx's per-swap overhead; one batched swap of three partials is
  no slower than three swaps.
- **Dispatcher planning** (`plan()`, jsdom, one id): 0.4 ms for 100 rows, 2.9 ms for 1,000,
  10.9 ms for 3,000. It is linear in the topic's live elements; browsers are faster than jsdom,
  but a 3,000-row grid on one topic should mark only its region live, not each row.
- **HTTP/1.1**: the direct burst queued 50 requests behind the browser's six-per-origin limit,
  one of which the stream holds. Batching is what keeps a burst from starving other requests.

## Patterns that recurred

| Pattern | Where | Direct code (evidence) | What covers it now |
|---|---|---|---|
| **Keyed instance refetch** (row, card, dot) | all six cases: `SeriesRow`, `SidebarPerson`, `DirectoryRow`, `PersonCard`, `DraftRow`, `BurstRow`, `JobCard`, `JobProgress`, `JobStep` (9 components) | each wrote a hand-built id (`"series-" + s.ID`), `data-key`, `hx-get` to **its own partial route**, a filtered trigger `lab:series[hit(detail, this.dataset.key)] from:#stream`, and `hx-swap="outerMorph"`; 5 routes (`/series/{id}/row`, `/people/{id}/{view}`, `/drafts/{id}`, `/burst/{id}`, `/jobs/{id}/{view}`) existed only to render one fragment | stage 2: `live.On(topic, key)` + the lab's `kid` filter + `live.Key(r)`; stage 3: `live.On` + structpages' `key` filter + `structpages.KeyOf(t)`. 14 routes → 10 (all 5 partial routes gone, one shell route added) |
| **Aggregate / region listener** | 1 summary tile, 4 count and list, 5 table | `hx-get={Page{} \|> url}` + `hx-trigger="topic from:#stream, resync from:#stream"` + swap, three times | `live.On(topic)`; resync is the dispatcher's |
| **Same entity in several components** | 2 (sidebar, directory, card), 6 (card, step line) | one request per component per frame, and two routes switching on a `{view}` path segment | batching: one request per page URL for all of them; each component still needs its Props branch |
| **Several keys of one component in one frame** | 1 (three rows), resync of any keyed list | one route request and one query per row | stage 3: one request; `live.Load` makes it one query |
| **Burst** | 5 | 50 frames, 50 requests | hub coalescing (per topic, before the buffer) + escalation to the region |
| **Resync** | every region | each region repeated `resync from:#stream`; rows did not, so a missed row frame stayed stale | `resync` refetches every outermost live element; `?since=` skips it when nothing was missed |

## Patterns that stayed case-specific

| Pattern | Where | Finding |
|---|---|---|
| **Editor protection** | 3 only | Direct needed a dirty check, a side-effecting trigger filter (`cleanOrWarn(this)`), a notice, and a refresh button with its own `hx-get` and `hx-swap="outerHTML"` (morph keeps the typed value when the server's value is unchanged). One case in the lab, but EOS lists about twenty editors (§7), so it stays in the dispatcher: `data-live-stale` and `data-live-refresh` are all a screen writes. |
| **Nested live elements** | 6 only | Direct sent two requests for one status change and swapped the step line twice (once alone, once inside the card). "Outer wins" fixes it for free once there is a dispatcher; it would not justify one on its own. |
| **Insert into an ordered list / delete** | 4 only | The direct version refetched the list on any `lab:files` frame: 1 request, correct order, deletions included. The library's (unknown id → `data-live-insert` region; row refetch → 404 → delete) costs the same requests and slightly fewer bytes. In stage 3 the 404 comes from the page handler for that one target inside the batch (`live.ErrNotFound` or the host's not-found). Precise *insertion* never happens: an insert is a region refetch either way. The 404 rule is worth keeping because it is free once batching exists; `data-live-insert` is "escalate unknown ids to this region". |
| **Layout-resident element** | 2 sidebar only | A page's Props cannot be expected to recognise shell components, so the sidebar row needs a home route (`shellPage`) and `data-live-url`. This is where the "no new routes" promise breaks, and in stage 3 also where "one request per frame" does (one GET per URL). EOS has more shell parts (bell, rail badges); see library-proposals §3. |
| **Own-write echo** | 3 | Saving your own row publishes a frame your own tab also hears, so it refetches the row it just swapped in (one wasted request, harmless). Suppressing it needs the tab's identity on the write request and an `except` on Publish; not built. |

## Exact per-component code (stage 3)

For a keyed live component the whole cost is:

```gsx
<tr id={burstPage.Row |> key(b.ID)} { live.On("lab:burst", b.ID)... }>…</tr>
```

```go
if t.Is(p.Row) {
	b, err := s.BurstRow(structpages.KeyOf(t))
	return nil, b, err
}
```

plus a `Publish` where the data changes, and the host's error handler mapping not-found to 404.
Optionally, for a list where one frame often names several rows:

```go
case t.Is(p.Row):
	row, err := live.Load(r.Context(), t, p.Row, s.SeriesByID)
	return seriesProps{}, nil, row, seriesSummary{}, err
```

Mount-wide, once: `WithTargetSelector(structpages.HTMXv4RenderTarget)` (unchanged from any htmx 4
app) and `WithMiddlewares(…, live.Middleware)`.

Library code by stage (non-test lines): stage 2 had `ids.go` 127 (keyed ids, selector) and
`batch.go` 186 (endpoint, URL checks, concurrency); stage 3 has `on.go` 16, `batch.go` 268
(middleware, trigger merging, aborts) and `load.go` 86. The keyed-id half moved into structpages;
the batch half grew by the response-header handling an endpoint never had to do, because each
target's headers now belong to one response.

What is still awkward:

- **Props signatures widen.** Each live component adds a result type, so `seriesPage.Props`
  returns five values, most of them zero in every branch. Returning
  `structpages.RenderComponent(p.Row(row))` from the branch avoids that, at the cost of leaving
  the normal flow.
- **Page middleware runs per target.** `live.Middleware` sits innermost among the global
  middlewares, so a page's own `Middlewares()` (a per-product licence check, say) runs once per
  target, not once per batch (`TestMiddlewareRunsTheOuterChainOnce`: outer 1, inner 3). Cheap
  here; see library-proposals §2.
- **The selector is configured twice.** `live.Load` needs to know which component each listed id
  names, so `live.Batch.Selector` must match the `WithTargetSelector` given to `Mount` (both
  default to `HTMXv4RenderTarget`). structpages does not expose the mount's selector to
  middleware.
- **`live.ErrNotFound` is a second not-found** the host's error handler must map to 404, beside
  its own.
- **An abort is the target's response verbatim.** A 3xx is followed by `fetch` before htmx sees it,
  so a page that redirects with a 3xx during an htmx request swaps nothing; a page that uses
  `HX-Location` (the structpages convention) aborts the batch cleanly.
- **A focused field the user has edited keeps its value through a morph**, by htmx's design; the
  dispatcher never morphs it anyway (dirty elements are spared). A focused field that is *not*
  edited takes the new value and keeps focus (e2e `a focused, unedited field … keeps focus`).

## Recommendations for design §4.4

Keep:

- `live.On` on the element; granularity is where it sits. It removed every hand-written trigger.
- Keyed component ids answered by the page's own route, no per-component routes (but see the
  shell exception).
- One GET per page URL per animation frame, answered by a page middleware. It is the largest
  measured win, runs authentication once, and keeps it correct by construction (stage 3).
- Outer wins, dirty protection with a notice, `resync` refetching outermost elements, and the
  404 → remove rule.
- Coalescing before the buffer, with "id-less wins" and the 16-id cap: it is what turns a burst
  into two frames.

Change:

- **Step 1 needs a region rule.** As written, an unkeyed live element matches every frame of its
  topic, and with "outer wins" a live table around live rows swallows every row change, so
  precision is impossible. The lab treats an unkeyed element containing keyed elements of the
  same topic as a *region*: refetched only for id-less frames, escalation, unknown ids
  (`data-live-insert`) and resync.
- **Step 7 on connect: skip the resync when nothing was missed.** §4.2 dropped `?since=` because
  `#eos-stream` survives boosted navigation. That holds for reconnects, but the connect-time
  resync then re-renders everything the server rendered milliseconds earlier (4 KB / 8 partials
  per load here, far more on an EOS page with a full rail). A hub position rendered into the
  stream URL (`hub.Since()`, unique per replica) removes it at no risk: the URL is fixed, so a
  reconnect after anything was published still resyncs.
- **Shell parts need `data-live-url`** (or a structpages hook, proposals §3); say so in §4.4
  rather than promising no new routes.
- **Escalation's "more than half" rule** fires on small regions (two of three rows), which is
  harmless; but with hub coalescing turning >16 ids into an id-less frame, `data-live-max` only
  matters for frames of 9–16 ids. Keep it as a minor rule, not as the burst defence.
- **Dropping unheard frames must also check `hx-trigger`** listeners (the bell keeps its own
  `hx-trigger="notify from:#eos-stream"`), or a non-live listener goes deaf. The lab's
  dispatcher does.

Drop or defer:

- `data-live-insert` described as insertion: it is region escalation for unknown ids. Keep the
  attribute (a second region of the same topic must not refetch) but not the framing.
- An in-process batch renderer inside structpages, and a batch endpoint: the page middleware
  needs neither and runs the outer chain once.
- `live.ID` / `live.Key` / `live.Selector` in `ds/live`: structpages v0.11.0 has `key`,
  `keytarget` and `KeyOf`. §4.4's `target.Key()` should read `structpages.KeyOf(target)`.

## Datastar vs htmx + live.js

`examples/datastar` (tag `datastar`) is the lab rewritten for Datastar 1.0.4 with
`github.com/starfederation/datastar-go` v1.2.2. It shares the store, the simulation and the hub with
the htmx lab (`examples/labdata`, `live.Hub`), so only the browser side and the push differ. Each
page opens one stream (`data-init="@get(…)"` on a hidden element) to its own `GET <page>/updates`
route; the route subscribes the viewer to the hub and, for every frame, renders what that page
shows for it and sends `datastar-patch-elements` (morphed by the elements' structpages `key` ids)
or `datastar-patch-signals`. Nothing is refetched. The comparison is with stage 3
(`structpages-keys`); every e2e journey of stage 3 has a Datastar twin (`e2e/datastar/`).

### Code per case and per component

Lines in the case sections (`examples/{lab,datastar}/cases*.gsx`, non-blank, non-comment, simulate
panels excluded; stage 3's shell route is outside the sections, Datastar's shell push is in
`stream.go`):

| Case | 1 | 2 | 3 | 4 | 5 | 6 | 8 | total |
|---|---|---|---|---|---|---|---|---|
| stage 3 | 55 | 42 | 51 | 39 | 28 | 36 | 57 | 308 |
| Datastar | 67 | 71 | 107 | 59 | 42 | 55 | 59 | 460 |

Shared code: stage 3 owns `live/` (`on.go` 9, `batch.go` 221, `load.go` 61, `hub.go` 191, `sse.go` 84)
and `js/live.js` (191). Datastar owns `stream.go` (about 120 lines without the meter's timing) and
reuses `hub.go`; the client is the library alone. So Datastar has about 450 fewer lines of
infrastructure and 150 more in the cases.

A keyed component in stage 3 declares itself and the page answers for it:

```gsx
<tr id={seriesPage.Row |> key(s.ID)} { live.On("lab:series", s.ID)... }>…</tr>
```

```go
case t.Is(p.Row):
	row, err := live.Load(r.Context(), t, p.Row, s.SeriesByID)
	return seriesProps{}, nil, row, seriesSummary{}, err
```

In Datastar the element carries only its id; the page's stream decides what a frame touches, so
every component a topic affects is named in that page's push function, and every page needs a
stream route and a resync branch:

```gsx
<tr id={seriesPage.Row |> key(s.ID)}>…</tr>
```

```go
type seriesPage struct {
	updates seriesUpdates `route:"GET /updates Series updates"`
}

func (seriesUpdates) ServeHTTP(w http.ResponseWriter, r *http.Request, s *store, h *hub, v viewer, d *dropper) error {
	p := seriesPage{}
	return serveStream(w, r, s, h, v, d, func(x *push, f live.Frame) error {
		switch {
		case f.Topic == resync || f.Topic == "lab:series" && f.IDs == nil:
			rows, sum := x.store.Series()
			return x.patch(p.Summary(sum), p.Rows(rows))
		case f.Topic == "lab:series":
			rows, err := x.store.SeriesByID(f.IDs) // the stream has every id at once: one query
			…
			for _, id := range f.IDs { … x.patch(p.Row(rows[id])) … }
			return x.patch(p.Summary(x.store.Summary()))
		}
		return nil
	})
}
```

That is the trade: stage 3 keeps "what is on this page" in the DOM (the dispatcher reads it),
Datastar keeps it in server code. It shows in case 2: the people stream must know which people
have cards (`var cards = []string{"p1", "p2"}`); a patch for an element the page does not show
logs `PatchElementsNoTargetsFound` in the console. It also shows in case 4: the stream cannot know
whether the tab already has a file id it hears about, so an existing id morphs the whole list
(insertion in order, unchanged rows kept by the morph) and a missing id is a `remove` patch. The
"outer wins" (case 6) and burst (case 5) rules, which the dispatcher implements generically, are
plain `switch` branches in the push function.

### Editor protection (case 3)

Datastar has no dirty-input protection: its morph sets `input.value` whenever the incoming
`value` attribute differs (`morphNode` in `library/src/plugins/watchers/patchElements.ts`), typed
or not. What a Datastar user does instead is make the editor signal-driven: the input is
`data-bind`-ed, the server never pushes the input, only `datastar-patch-signals` with the saved
value, and one expression on the container decides per row:

```gsx
<div data-signals=js`@{draftSignals(rows, v)}`  // {drafts: {d1: {value, base, server, by, stale}}}
	data-on-signal-patch="Object.keys(patch.drafts).forEach(id => (r => (r.stale = r.value !== r.base && r.value !== r.server) || (r.value = r.base = r.server))($drafts[id]))"
	data-on-signal-patch-filter="{include: /^drafts\.[^.]+\.server$/}">
```

```gsx
<input … data-bind=f`drafts.@{d.ID}.value`/>
<span data-show=js`!!$drafts[@{d.ID}].by` data-text=js`'last saved by ' + $drafts[@{d.ID}].by`>…</span>
<p data-stale style="display: none" data-show=js`$drafts[@{d.ID}].stale`>Changed by someone else ·
	<button type="button" data-on:click=js`const r = $drafts[@{d.ID}]; r.value = r.base = r.server; r.stale = false`>Refresh</button></p>
```

It passes the same e2e as stage 3 (dirty row warns and keeps its caret, a neighbour updates, an
unedited focused field takes the new value and keeps focus, your own save is not mistaken for
someone else's), and it is the cheapest change on the wire (94 bytes against 717). But it is
per-screen plumbing (four signals per row, the guard expression, the save handler answering with
signals) where stage 3's screens write `data-live-stale` and `data-live-refresh` and the
dispatcher does the rest for every form. The guard also had to be one expression: Datastar splits a
`returnsValue` expression on every `;` outside strings, so a block body with `;` breaks it.

### Requests, bytes, latency

Per tab (the table above), Datastar makes **no requests** for any live change, and its bytes are
20–30 % lower (no `<hx-partial>` wrapper, and the htmx numbers do not even include the frame or
HTTP headers). Case 3 is 8 × smaller because a signal replaces the row. Swap times are within
noise of each other. The one case where Datastar sends *more* events is a frame touching several
elements: one event per element, because Datastar parses an event's elements inside a
`<template>`, whose first element fixes the parsing mode, so a `<tr>` or `<tbody>` sharing an event
with a `<section>` is silently dropped (found by the e2e: the resync lost every row). The events
are on an open stream, so this costs ~50 bytes each, not a round trip.

Latency: one tab, in-process (`go test -run TestFanOut` with `FANOUT_N=1`): change → tab updated
**0.26 ms** for Datastar against **1.24 ms** for signal + pull, which has to make a request after
the frame. On a real network the pull adds a full round trip per change.

### Server cost on fan-out (case 7)

`examples/{lab,datastar}/fanout_test.go` open N streams against the real handler (`httptest`), make
one change to a series row, and read every tab to completion; stage 3's simulated tab does what
`js/live.js` does (one batch GET for `series-row--s3 series-summary`). Medians of 7 rounds, N = 50
(`docs/measurements/fanout-50.json`):

| | Requests | Bytes (all tabs) | Brotli bytes | Queries | Renders | CPU ms | Change → tab p50 / max |
|---|---|---|---|---|---|---|---|
| stage 3: signal + pull | 50 | 35,150 | 15,850 | 100 | 100 | 79.7 | 11.2 / 13.4 ms |
| Datastar: push HTML | 0 | 27,300 | — | 100 | 100 | 11.0 | 1.0 / 1.4 ms |
| Datastar, brotli stream | 0 | 27,300 | 15,750 | 100 | 100 | 13.4 | 0.8 / 1.2 ms |

At N = 200: 397.7 against 45.9 CPU ms, p50 27.9 against 2.4 ms. CPU is the whole test process from
the change until every tab is updated, so it includes the simulated tabs; stage 3's tabs do more
of it (an HTTP request each), so the server-only gap is smaller than 7×, but every one of those 50
requests is also a full trip through routing, middleware and authentication on the server.

What does *not* differ: **renders and queries**. Both render the row and the summary once per
subscriber (100 renders, 100 queries for 50 tabs). Datastar renders inside each subscriber's stream
goroutine, for that subscriber's viewer, which is what makes per-viewer output correct (case 3's
"last saved by you" is rendered per viewer on the stream; `TestDraftSignalsAreRenderedPerViewer`
and the two-context e2e check it). Rendering once and writing the same bytes to every stream is an
optimisation a push model makes easy (a pull model would need a response cache), but only for output that is provably
viewer-independent, and the app has to decide that per component; it was not built. Brotli on the
stream (`datastar.WithCompression`) costs some CPU per stream and gave the same ratio as
compressing each pulled response on its own (546 → 315 bytes per tab against 703 → 317): one small
change is not enough history for the long-lived stream's window to pay.

### URL and history (case 8)

A filtered list whose filter is in the URL (`/catalog?status=failing`), live, with Back, Forward
and reload; the same Playwright journey (`e2e/catalog.shared.ts`) passes against both.

- **Stage 3**: the filter links are `hx-boost`-ed, so htmx pushes the URL and restores it on Back
  by refetching the page. Nothing live-specific was needed: the dispatcher refetches against
  `location.pathname + location.search`, so every refetch carries the filter; a row that leaves the
  filter answers 404 and is removed, a row that enters it is an unknown id and refetches the list
  (`data-live-insert`). A filter click costs one GET of the page (4,974 bytes).
- **Datastar**: the shareable-URL attributes are Pro (`data-query-string` with `__history`,
  `data-replace-url`), and the Pro licence forbids public repositories and open-source projects, so
  this public repo cannot use them at any price. Without them the page keeps the URL and the signal
  in step itself: the link sets `$query.status` and calls `history.pushState`, a
  `data-on:popstate__window` sets the signal back from `location.search`, and the stream element
  restarts its `@get` on any `query.*` signal patch (same URL, so Datastar aborts the old stream).
  The stream reads the filter from the signals and answers with the filtered list, so a filter
  change is a new stream connection, not a request (1,502 bytes, the list plus the sidebar's
  resync). Live changes morph the whole filtered list: like case 4, the stream cannot know which
  rows the tab shows. There is no boosted navigation in Datastar; ordinary links are full page
  loads, which is its recommended model.

### Failure and reconnect

Both stages close the stream in a hidden tab and reopen it when visible (htmx `pauseOnBackground`,
Datastar `openWhenHidden: false` for `@get`), and both resync on reconnect via `?since=`. The
difference is the defaults, read from the sources and checked by the "reconnect" e2e in both
projects (`/sim/drop-streams` ends every stream and refuses new ones for a second):

- htmx 4's SSE extension reconnects after any end of stream, with backoff from 500 ms to 60 s
  and **no attempt limit**.
- Datastar's `@get` defaults to `retry: 'auto'`: after a *clean* end of stream (a server restart
  that closes the response gracefully) it **does not reconnect at all**; after a network error it
  retries from 1 s up to 30 s and gives up for good after 10 attempts (`retries-failed`). The e2e
  "a stream with Datastar's default retry stays closed after a clean end" shows it. A live page has
  to say `retry: 'always', retryMaxCount: Infinity`, as the lab's layout does.
- A Datastar stream checks the viewer once, at connect; later pushes run as that viewer. A
  permission change mid-stream (access revoked) must be enforced in the push or by ending the
  stream. Stage 3 re-runs authentication and Props on every refetch.

### Dependency and licensing

- Datastar core is MIT. The 1.x bundle is **not on npm** (the npm `latest` is `1.0.0-beta.11`), so
  the lab vendors `bundles/datastar.js` from the GitHub tag v1.0.4
  (`examples/datastar/web/vendor/datastar-1.0.4.js`, source-map comment removed, licence beside
  it). 13.4 KB gzipped, against 17.5 KB for htmx 4 + the SSE extension + `live.js`.
- `datastar-go` v1.2.2 adds `CAFxX/httpcompression`, `klauspost/compress`, `andybalholm/brotli` and
  `valyala/bytebufferpool` to the module graph. The lab uses only `NewSSE`, `PatchElements`,
  `MarshalAndPatchSignals`, `RemoveElementByID`, `ReadSignals` and `Send`; a host could write the
  two event formats itself in about 40 lines.
- Pro ($349 solo, $1,299 for up to 25 employees, lifetime, no public or open-source repositories)
  holds the attributes this comparison would have wanted: `data-query-string` and
  `data-replace-url` (case 8). `data-persist`, `data-scroll-into-view`, `data-view-transition` and
  the Inspector were not needed. Rocket moved out of Pro into a free bundle in 1.0.4.
- gsx's JavaScript lexer rejects a hole inside Datastar's `@action(…)` syntax (`@` is not
  JavaScript), so Go values reach expressions through data attributes
  (`data-url={…|> url} data-on:click="@post(el.dataset.url, …)"`); holes in plain signal
  expressions (`$drafts[@{d.ID}].stale`) work.

### Verdict per dimension

| Dimension | Better | Why |
|---|---|---|
| Requests per live change | **Datastar** | none, against one GET per page URL per frame |
| Latency | **Datastar** | no round trip after the frame: 0.3 against 1.2 ms in-process, a full RTT on a network |
| Server CPU on fan-out | **Datastar** | ~7× less process CPU at N = 50 and 200, no per-tab request through the middleware chain |
| Renders / queries on fan-out | even | one render per subscriber in both; Datastar could share a viewer-independent render in-process, the pull model would need a response cache |
| Bytes | Datastar, slightly | 20–30 % fewer per change; brotli evens them out at this size |
| Code per component | **stage 3** | an attribute and a Props branch; Datastar names every affected component in a per-page push function, plus a stream route and resync per page (460 against 308 lines) |
| Infrastructure to own | **Datastar** | the library replaces `js/live.js` and the batch middleware; ~450 fewer lines |
| Editor protection | **stage 3** | generic in the dispatcher; Datastar needs signal plumbing per editor, though it is the smallest on the wire |
| Insert, delete, nested, burst | even | server-side `switch` branches against dispatcher rules; both correct |
| URL / history | **stage 3** | free with `hx-boost` and `location.search`; Datastar needs Pro (unusable here) or hand-written `pushState`/`popstate` |
| Reconnect | stage 3 | safe defaults; Datastar's defaults stop a live page after a graceful restart |
| Authorisation | stage 3 | every refetch is an authenticated request; a stream is authorised once |
| Dependencies / licence | stage 3 | htmx on npm; Datastar 1.x vendored, useful parts of Pro closed to open source |

What Datastar does clearly better: it never makes a request for a live change, so latency and
server CPU per change are an order of magnitude lower and grow far more slowly with viewers, and the
browser side needs no dispatcher. Where it costs more is developer code: the server must know what
every page shows, and generic behaviours stage 3 gets for free (dirty editors, URL state, refetch
through the normal handler) become per-page code. For EOS, where screens are many and live
elements are many per screen, stage 3 stays the better default; Datastar's push model is worth
copying for one hot, wide-audience element (a presence or progress feed) where the per-viewer
request cost dominates.
