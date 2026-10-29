# Findings

What the lab showed about the EOS realtime design §4.3–4.4
(`eos/docs/superpowers/specs/2026-10-29-eos-realtime-design.md`). The work went evidence first:
the six cases were written directly (tag `direct`: per-element `hx-trigger`, a lab-local
broadcaster, no library), measured, and only then were the recurring parts extracted into
`live/` and `js/live.js` and the cases ported onto them.

## Measurements

One tab, Chromium headless on localhost, in-memory store, after the page settled. "Server ms" is
the handler's time to first byte summed over the tab's requests (`Server-Timing`); "swap ms" is
`htmx:before:swap` → `htmx:after:swap`. Raw data: `docs/measurements/{direct,live}.json`;
reproduce with `LAB_VARIANT=… npx playwright test -c e2e measure.spec.ts` (direct: check out the
`direct` tag).

| Case: action | Direct: frames / requests / bytes / swaps | Library: frames / requests / bytes / swaps | Server ms (direct → library) | Swap ms (direct → library) |
|---|---|---|---|---|
| 1 one of ten series rows changes | 1 / 2 / 668 / 2 | 1 / **1** / 663 / 2 | 0.07 → 0.27 | 0.6 → 0.9 |
| 2 one person, shown in three places | 1 / 3 / 1056 / 3 | 1 / **1** / 1068 / 3 | 0.10 → 0.44 | 1.0 → 1.0 |
| 3 neighbour draft row edited | 1 / 1 / 819 / 1 | 1 / 1 / 717 / 1 | 0.06 → 0.18 | 0.9 → 0.6 |
| 4 upload, then delete | 2 / 2 / 1621 / 2 | 2 / 2 / 1436 / 4 | 0.05 → 0.28 | 0.6 → 1.1 |
| 5 fifty rows within 100 ms | 50 / 50 / 9514 / 50 | **2** / **2** / 7171 / 2 | 0.76 → 0.28 | 3.7 → 1.6 |
| 6 job progress tick | 1 / 1 / 313 / 1 | 1 / 1 / 304 / 1 | 0.05 → 0.15 | 0.7 → 0.6 |
| 6 job status change | 1 / 2 / 1085 / 2 | 1 / **1** / 704 / **1** | 0.09 → 0.16 | 0.9 → 0.7 |
| resync on connect (series page) | 0 / 1 / 424 / 1 | 0 / 0 / 0 / 0 | — | — |

Before the `?since=` change the library's connect resync cost 1 request, 4070 bytes and 8 swaps
on the series page (every outermost live element, including the six sidebar rows), on every
page load.

Reading the numbers:

- **Requests** are where the design pays: one per frame batch, so cases 1, 2 and 6 halve or
  better, and the burst drops from 50 to 2 (the first frame of the burst goes at once, the rest
  coalesce in the hub into one id-less frame that refetches the table).
- **Bytes** barely move. The `<hx-partial>` wrapper (~60 bytes per item) offsets what one
  request saves over several. Batching saves round trips and connection slots, not bandwidth.
- **Server time** is sub-millisecond either way here; the library's is slightly higher per
  request because one request now renders several items through the full handler chain. The
  in-process re-entry is not a cost worth optimising (library-proposals §2).
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
| **Keyed instance refetch** (row, card, dot) | all six cases: `SeriesRow`, `SidebarPerson`, `DirectoryRow`, `PersonCard`, `DraftRow`, `BurstRow`, `JobCard`, `JobProgress`, `JobStep` (9 components) | each wrote a hand-built id (`"series-" + s.ID`), `data-key`, `hx-get` to **its own partial route**, a filtered trigger `lab:series[hit(detail, this.dataset.key)] from:#stream`, and `hx-swap="outerMorph"`; 5 routes (`/series/{id}/row`, `/people/{id}/{view}`, `/drafts/{id}`, `/burst/{id}`, `/jobs/{id}/{view}`) existed only to render one fragment | `live.On(topic, key)` + the `kid` filter + one `t.Is(p.Row)` branch reading `live.Key(r)`; 14 routes → 10 (all 5 partial routes gone, one shell route added) |
| **Aggregate / region listener** | 1 summary tile, 4 count and list, 5 table | `hx-get={Page{} \|> url}` + `hx-trigger="topic from:#stream, resync from:#stream"` + swap, three times | `live.On(topic)`; resync is the dispatcher's |
| **Same entity in several components** | 2 (sidebar, directory, card), 6 (card, step line) | one request per component per frame, and two routes switching on a `{view}` path segment | batching: one request for all of them; each component still needs its Props branch |
| **Burst** | 5 | 50 frames, 50 requests | hub coalescing (per topic, before the buffer) + escalation to the region |
| **Resync** | every region | each region repeated `resync from:#stream`; rows did not, so a missed row frame stayed stale | `resync` refetches every outermost live element; `?since=` skips it when nothing was missed |

## Patterns that stayed case-specific

| Pattern | Where | Finding |
|---|---|---|
| **Editor protection** | 3 only | Direct needed a dirty check, a side-effecting trigger filter (`cleanOrWarn(this)`), a notice, and a refresh button with its own `hx-get` and `hx-swap="outerHTML"` (morph keeps the typed value when the server's value is unchanged). One case in the lab, but EOS lists about twenty editors (§7), so it stays in the dispatcher: `data-live-stale` and `data-live-refresh` are all a screen writes. |
| **Nested live elements** | 6 only | Direct sent two requests for one status change and swapped the step line twice (once alone, once inside the card). "Outer wins" fixes it for free once there is a dispatcher; it would not justify one on its own. |
| **Insert into an ordered list / delete** | 4 only | The direct version refetched the list on any `lab:files` frame: 1 request, correct order, deletions included. The library's (unknown id → `data-live-insert` region; row refetch → 404 → delete) costs the same requests and slightly fewer bytes. Precise *insertion* never happens: an insert is a region refetch either way. The 404 rule is worth keeping because it is free once batching exists; `data-live-insert` is "escalate unknown ids to this region". |
| **Layout-resident element** | 2 sidebar only | A page's Props cannot be expected to recognise shell components, so the sidebar row needs a home route (`shellPage`) and `data-live-url`. This is where the "no new routes" promise breaks. EOS has more shell parts (bell, rail badges); see library-proposals §3. |
| **Own-write echo** | 3 | Saving your own row publishes a frame your own tab also hears, so it refetches the row it just swapped in (one wasted request, harmless). Suppressing it needs the tab's identity on the write request and an `except` on Publish; not built. |

## Exact per-component code

For a keyed live component the whole cost is:

```gsx
<tr id={seriesPage.Row |> kid(s.ID)} { live.On("lab:series", s.ID)... }>…</tr>
```

```go
case t.Is(p.Row):
	row, err := s.OneSeries(live.Key(r))
	return seriesProps{}, nil, row, seriesSummary{}, err
```

plus a `Publish` where the data changes, and the host's error handler mapping not-found to 404.

What was awkward:

- **Props signatures widen.** Each live component adds a result type, so `seriesPage.Props`
  returns five values, most of them zero in every branch. Returning
  `structpages.RenderComponent(p.Row(row))` from the branch avoids that, at the cost of leaving
  the normal flow.
- **Two components of one type** (`peoplePage.Row` and `.Card` both take `person`) work only
  because one request renders one of them; a batch item is one request, so that holds.
- **The keyed id and `Key` live outside structpages**, through `live.Selector`, which rewrites
  `HX-Target` before the stock selector sees it. Stock structpages silently serves the whole
  page into the row (`TestStockSelectorMissesKeyedTargets`); the batch handler's root-id check
  is what stops that reaching the DOM.
- **A focused field the user has edited keeps its value through a morph**, by htmx's design; the
  dispatcher never morphs it anyway (dirty elements are spared). A focused field that is *not*
  edited takes the new value and keeps focus (e2e `a focused, unedited field … keeps focus`).

## Recommendations for design §4.4

Keep:

- `live.On` on the element; granularity is where it sits. It removed every hand-written trigger.
- Keyed component ids answered by the page's own route, no per-component routes (but see the
  shell exception).
- One POST per animation frame through the full handler chain. It is the largest measured win
  and keeps auth correct by construction.
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
- An in-process batch renderer inside structpages: re-entering the HTTP stack is cheap
  (sub-millisecond per item here) and is what applies auth, licensing and middleware.
