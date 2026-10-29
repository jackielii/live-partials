# Findings

What the lab showed about the EOS realtime design §4.3–4.4
(`eos/docs/superpowers/specs/2026-10-29-eos-realtime-design.md`). The work went evidence first,
in three tagged stages: the six cases written directly (`direct`: per-element `hx-trigger`, a
lab-local broadcaster, no library); the recurring parts extracted into `live/` and `js/live.js`
(`extracted-v1`: own keyed ids, a `POST /live` batch endpoint); then structpages v0.11.0's keyed
ids and a page middleware in place of the endpoint (`structpages-keys`).

## Measurements

One tab, Chromium headless on localhost, in-memory store, after the page settled. "Server ms" is
the handler's time to first byte summed over the tab's requests (`Server-Timing`); "swap ms" is
`htmx:before:swap` → `htmx:after:swap`; "queries" counts store calls (each exported store method is
one query), reported per response in `X-Queries`. Stage 3 values are the median of three runs; the
counts were identical in every run. Raw data: `docs/measurements/{direct,extracted-v1,structpages-keys}.json`;
reproduce with `LAB_VARIANT=… npx playwright test -c e2e measure.spec.ts` at each tag.

Requests / response bytes / swaps per stage (1 `direct` → 2 `extracted-v1` → 3 `structpages-keys`):

| Case: action | Requests 1 → 2 → 3 | Bytes 1 → 2 → 3 | Swaps 1 → 2 → 3 | Queries (3) | Server ms 1 → 2 → 3 | Swap ms 1 → 2 → 3 |
|---|---|---|---|---|---|---|
| 1 one of ten series rows changes | 2 → **1** → 1 | 668 → 663 → 663 | 2 → 2 → 2 | 2 | 0.07 → 0.27 → 0.10 | 0.6 → 0.9 → 1.0 |
| 1 three rows change in one frame | — → — → 1 | — → — → 1139 | — → — → 4 | **2** (4 without `live.Load`) | — → — → 0.14 | — → — → 1.1 |
| 2 one person, shown in three places | 3 → **1** → 2 | 1056 → 1068 → 1068 | 3 → 3 → 3 | 3 | 0.10 → 0.44 → 0.18 | 1.0 → 1.0 → 1.1 |
| 3 neighbour draft row edited | 1 → 1 → 1 | 819 → 717 → 717 | 1 → 1 → 1 | 1 | 0.06 → 0.18 → 0.11 | 0.9 → 0.6 → 0.8 |
| 4 upload, then delete | 2 → 2 → 2 | 1621 → 1436 → 1436 | 2 → 4 → 4 | 4 | 0.05 → 0.28 → 0.20 | 0.6 → 1.1 → 1.1 |
| 5 fifty rows within 100 ms | 50 → **2** → 2 | 9514 → 7171 → 7171 | 50 → 2 → 2 | 2 | 0.76 → 0.28 → 0.23 | 3.7 → 1.6 → 1.5 |
| 6 job progress tick | 1 → 1 → 1 | 313 → 304 → 304 | 1 → 1 → 1 | 1 | 0.05 → 0.15 → 0.07 | 0.7 → 0.6 → 0.8 |
| 6 job status change | 2 → **1** → 1 | 1085 → 704 → 704 | 2 → **1** → 1 | 1 | 0.09 → 0.11 → 0.07 | 0.9 → 0.7 → 0.9 |
| resync on connect (series page) | 1 → 0 → 0 | 424 → 0 → 0 | 1 → 0 → 0 | 0 | — | — |

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
