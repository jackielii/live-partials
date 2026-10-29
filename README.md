# live-partials

Server-pushed live updates for Go + [structpages](https://github.com/jackielii/structpages) +
[gsx](https://github.com/gsxhq/gsx) + htmx 4 apps, and a lab that measures them on six screens
modelled on real ones.

## The model

The server never pushes content. When data changes it publishes a **frame**: a topic and, at
most, opaque ids (`orbit:data ["s3"]`) on one SSE stream per tab. Elements that can render
themselves again are marked live in the template (`live.On("orbit:data", row.ID)`), with an id
that names their page component and key. A small dispatcher in the page matches each frame to
the elements showing those ids, keeps only the outermost, escalates to the enclosing region when
too many match, leaves elements with unsaved input alone, and sends every refetch of an
animation frame as **one** `POST /live`. The server runs each one as an ordinary in-process
partial request through the app's full handler chain (so auth applies) and answers with
`<hx-partial>` fragments that htmx morphs in. Granularity is wherever the attribute sits.

## Install

```sh
go get github.com/jackielii/live-partials
```

`js/live.js` is plain ESM with no dependencies; import it from your bundle (or copy it).
It needs htmx 4 and its SSE extension (`htmx.org/dist/ext/hx-sse.js`).

## Use in a host app

Server:

```go
hub := live.NewHub[*User](live.Config{})

mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
	hub.ServeSSE(w, r, currentUser(r))
})
mux.Handle("POST /live", live.BatchHandler(app)) // app: the full chain, auth included

structpages.Mount(mux, pages{}, "/", "App",
	structpages.WithTargetSelector(live.Selector(structpages.HTMXv4RenderTarget)),
	structpages.WithArgs(hub), …)

// after a write commits
hub.Publish(ctx, canSeeSeries, "orbit:data", series.ID)
```

`canSeeSeries` is any `live.Audience[*User]`: it gets every subscriber's viewer at once and
returns one verdict each. `live.AudienceFunc` adapts a per-viewer predicate.

`gsx.toml`, beside the structpages filters:

```toml
kid = "github.com/jackielii/live-partials/live.ID"
```

Template and Props:

```gsx
component (p seriesPage) Rows(rows []series) {
	<tbody id={seriesPage.Rows |> id} { live.On("orbit:data")... }>
		{ for _, s := range rows { <p.Row s={s}/> } }
	</tbody>
}

component (p seriesPage) Row(s series) {
	<tr id={seriesPage.Row |> kid(s.ID)} { live.On("orbit:data", s.ID)... }>…</tr>
}
```

```go
case t.Is(p.Row):
	row, err := store.Series(live.Key(r)) // a not-found error must become a 404: the row is removed
	return …, row, …, err
```

Page:

```gsx
<div id="stream" hx-sse:connect=f`/events?since=@{hub.Since()}` hidden></div>
```

```js
import "htmx.org/dist/ext/hx-sse.js";
import { start } from "live-partials/js/live.js";
start({ stream: "#stream" });
```

Attributes the dispatcher reads:

| Attribute | Meaning |
|---|---|
| `data-live="t1 t2"` | topics the element listens to (`live.On`) |
| `data-live-key="k"` | the instance key; without it the element matches every frame of its topic |
| `data-live-url` | URL to refetch from, when the element is not rendered by the current page (shell parts) |
| `data-live-max="8"` | on a region: escalate past this many matching keyed elements |
| `data-live-insert` | on a region: refetch it for ids that match no element (new rows) |
| `data-live-stale` | the notice revealed when a change hits an element with unsaved input |
| `data-live-refresh` | a button inside the element that discards the input and refetches |

A *region* is an unkeyed live element containing keyed live elements of the same topic. It is
refetched for an id-less frame, an escalation, an unknown id (with `data-live-insert`) or a
resync, never for an ordinary keyed change.

## Durable sources

The hub keeps nothing. A frame published while a tab is away is gone, and the tab's `resync`
on reconnect covers it (`?since=` skips that resync when nothing was published since the page
rendered). A host with events that must not be missed (messages, notifications) writes its own
SSE handler: replay its outbox after `Last-Event-ID` with `live.WriteEvent` (with ids), then
`hub.Subscribe(viewer)` and select over its own feed and `Subscription.Ready()`, writing topic
frames with `live.WriteFrame`. Topic frames carry no id, so they never move the cursor.

## How EOS would wire it

- `internal/platform/realtime` holds a `live.Hub[*auth.Viewer]`. The `pg_notify` listener on
  each replica calls `hub.Publish` with the audience it decoded (`Users`, `Product`, `Entity`
  as `live.Audience` implementations; `Entity` batches `CanSee` over the viewers it is given).
- `GET /events` stays the platform's merged handler (design §4.1): it replays the durable
  sources and selects over `Subscription.Ready()` for topics.
- `internal/app` mounts `POST /live` with `live.BatchHandler(theWholeChain)` and wraps the target
  selector with `live.Selector`; `ds/live` becomes a re-export or disappears.
- `frontend/js/live.js` calls `start({ stream: "#eos-stream" })`.

See [docs/findings.md](docs/findings.md) for what the lab measured and
[docs/library-proposals.md](docs/library-proposals.md) for what belongs in structpages, gsx or
here.

## Lab

```sh
npm install
npm run dev            # gsx dev: Vite + Go with reload, http://localhost:5173
```

Six cases, each with a "simulate other users" panel and a meter of frames, requests, bytes,
swaps, server and swap time for the tab. The first commit (tag `direct`) has the same cases
written directly with per-element `hx-trigger`, for comparison.

## Test

```sh
go test ./...                     # hub, batch handler, keyed ids
npm run test:js                   # dispatcher rules (node --test + jsdom)
npm run e2e                       # Playwright: the six cases, two tabs, caret, dispatcher costs
LAB_VARIANT=live npx playwright test -c e2e measure.spec.ts   # the numbers in docs/findings.md
```
