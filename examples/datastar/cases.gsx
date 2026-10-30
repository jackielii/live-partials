package main

import (
	"net/http"
	"slices"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/jackielii/live-partials/live"
)

// Home

type homePage struct{}

component (p homePage) Page(s *store, h *hub) {
	<Layout title="Six cases, Datastar" stream="/series/updates" s={s} h={h} panel={<p class="text-stone-600">Open a case.</p>}>
		<p class="max-w-prose">
			The same cases as the htmx lab, written the Datastar way: each page holds one stream and the server
			pushes the changed elements down it.
		</p>
	</Layout>
}

// Case 1: keyed list + aggregate

type seriesPage struct {
	updates seriesUpdates `route:"GET /updates Series updates"`
}

type seriesProps struct {
	rows []series
	sum  seriesSummary
}

func (p seriesPage) Props(s *store) (seriesProps, error) {
	rows, sum := s.Series()
	return seriesProps{rows, sum}, nil
}

component (p seriesPage) Page(props seriesProps, s *store, h *hub) {
	<Layout title="Series catalog" stream={seriesUpdates{} |> url} s={s} h={h} panel={<seriesPanel/>}>
		<p.Summary sum={props.sum}/>
		<table class="w-full text-sm" data-testid="series">
			<thead><tr class="text-left text-stone-500"><th>Series</th><th>Points</th><th>Status</th></tr></thead>
			<p.Rows rows={props.rows}/>
		</table>
	</Layout>
}

component (p seriesPage) Summary(sum seriesSummary) {
	<section id={seriesPage.Summary |> id} class="grid grid-cols-4 gap-2 rounded border border-stone-200 bg-white p-3 text-sm" data-testid="summary">
		<div>Series <b data-testid="sum-count">{ sum.Count }</b></div>
		<div>Points <b data-testid="sum-points">{ sum.Points }</b></div>
		<div>Stale <b>{ sum.Stale }</b></div>
		<div>Failing <b data-testid="sum-failing">{ sum.Failing }</b></div>
	</section>
}

component (p seriesPage) Rows(rows []series) {
	<tbody id={seriesPage.Rows |> id}>
		{ for _, s := range rows {
			<p.Row s={s}/>
		} }
	</tbody>
}

component (p seriesPage) Row(s series) {
	<tr id={seriesPage.Row |> key(s.ID)}>
		<td>{ s.Name }</td>
		<td data-testid="points">{ s.Points }</td>
		<td data-testid="status">{ s.Status }</td>
	</tr>
}

type seriesUpdates struct{}

func (seriesUpdates) ServeHTTP(w http.ResponseWriter, r *http.Request, s *store, h *hub, v viewer, d *dropper) error {
	p := seriesPage{}
	return serveStream(w, r, s, h, v, d, func(x *push, f live.Frame) error {
		switch {
		case f.Topic == resync || f.Topic == "lab:series" && f.IDs == nil:
			rows, sum := x.store.Series()
			return x.patch(p.Summary(sum), p.Rows(rows))
		case f.Topic == "lab:series":
			rows, err := x.store.SeriesByID(f.IDs)
			if err != nil {
				return err
			}
			for _, id := range f.IDs {
				if row, ok := rows[id]; ok {
					if err := x.patch(p.Row(row)); err != nil {
						return err
					}
				}
			}
			return x.patch(p.Summary(x.store.Summary()))
		}
		return nil
	})
}

component seriesPanel() {
	<SimButton action="series-touch" id="s3">Change series s3</SimButton>
	<SimButton action="series-touch" id="s7">Change series s7</SimButton>
	<SimButton action="series-touch" id="s2 s5 s8">Change series s2, s5 and s8 together</SimButton>
}

// Case 2: one person in many places

type peoplePage struct {
	updates peopleUpdates `route:"GET /updates People updates"`
}

// cards are the people the page shows as cards. The stream must know what
// the page shows: a card patch for someone without a card finds no target.
var cards = []string{"p1", "p2"}

component (p peoplePage) Page(s *store, h *hub) {
	<Layout title="People" stream={peopleUpdates{} |> url} s={s} h={h} panel={<peoplePanel/>}>
		<div class="flex gap-4">
			{ for _, id := range cards {
				{ if pr, err := s.Person(id); err == nil {
					<p.Card pr={pr}/>
				} }
			} }
		</div>
		<table class="w-full text-sm" data-testid="directory">
			<thead><tr class="text-left text-stone-500"><th>Name</th><th>Presence</th></tr></thead>
			<p.Rows people={s.People()}/>
		</table>
	</Layout>
}

component (p peoplePage) Rows(people []person) {
	<tbody id={peoplePage.Rows |> id}>
		{ for _, pr := range people {
			<p.Row pr={pr}/>
		} }
	</tbody>
}

component (p peoplePage) Row(pr person) {
	<tr id={peoplePage.Row |> key(pr.ID)}>
		<td>{ pr.Name }</td>
		<td class="flex items-center gap-2"><presenceDot state={pr.Presence}/>{ pr.Presence }</td>
	</tr>
}

component (p peoplePage) Card(pr person) {
	<article id={peoplePage.Card |> key(pr.ID)} class="w-56 rounded border border-stone-200 bg-white p-3">
		<h3 class="flex items-center gap-2 font-semibold"><presenceDot state={pr.Presence}/>{ pr.Name }</h3>
		<p class="text-xs text-stone-500">Currently { pr.Presence }</p>
	</article>
}

type peopleUpdates struct{}

func (peopleUpdates) ServeHTTP(w http.ResponseWriter, r *http.Request, s *store, h *hub, v viewer, d *dropper) error {
	p := peoplePage{}
	return serveStream(w, r, s, h, v, d, func(x *push, f live.Frame) error {
		ids := f.IDs
		switch {
		case f.Topic == resync || f.Topic == "lab:presence" && ids == nil:
			if err := x.patch(p.Rows(x.store.People())); err != nil {
				return err
			}
			ids = cards
		case f.Topic != "lab:presence":
			return nil
		}
		for _, id := range ids {
			pr, err := x.store.Person(id)
			if err != nil {
				continue
			}
			if f.Topic != resync {
				if err := x.patch(p.Row(pr)); err != nil {
					return err
				}
			}
			if slices.Contains(cards, id) {
				if err := x.patch(p.Card(pr)); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

component peoplePanel() {
	<SimButton action="presence" id="p1">Ada changes presence</SimButton>
	<SimButton action="presence" id="p4">Edsger changes presence</SimButton>
}

// Case 3: editor with unsaved input. The input is bound to a signal, and the
// server pushes the saved value as a signal, never the input: a row whose
// value differs from what it was loaded with keeps it and shows the notice.

type draftsPage struct {
	updates draftsUpdates `route:"GET /updates Draft updates"`
}

// draftSignal is one row's signals: the bound value, the value it was loaded
// with (base), the last saved value (server), who saved it, and whether a
// save by someone else is waiting behind unsaved input.
type draftSignal struct {
	Value  string `json:"value,omitempty"`
	Base   string `json:"base,omitempty"`
	Server string `json:"server"`
	By     string `json:"by"`
	Stale  *bool  `json:"stale,omitempty"`
}

// savedBy is how v sees who saved d.
func savedBy(d draftRow, v viewer) string {
	if d.By == string(v) {
		return "you"
	}
	return d.By
}

func draftSignals(rows []draftRow, v viewer) map[string]any {
	m := map[string]draftSignal{}
	no := false
	for _, d := range rows {
		m[d.ID] = draftSignal{Value: d.Value, Base: d.Value, Server: d.Value, By: savedBy(d, v), Stale: &no}
	}
	return map[string]any{"drafts": m}
}

component (p draftsPage) Page(s *store, h *hub, v viewer) {
	{{ rows := s.Drafts() }}
	<Layout title="Report draft" stream={draftsUpdates{} |> url} s={s} h={h} panel={<draftsPanel/>}>
		<p class="text-sm text-stone-600">Edit a value and leave it unsaved; changes by others to that row warn instead of overwriting.</p>
		<div
			class="flex flex-col gap-2"
			data-testid="drafts"
			data-signals=js`@{draftSignals(rows, v)}`
			data-on-signal-patch="Object.keys(patch.drafts).forEach(id => (r => (r.stale = r.value !== r.base && r.value !== r.server) || (r.value = r.base = r.server))($drafts[id]))"
			data-on-signal-patch-filter="{include: /^drafts\.[^.]+\.server$/}"
		>
			{ for _, d := range rows {
				<p.Row d={d} v={v}/>
			} }
		</div>
	</Layout>
}

component (p draftsPage) Row(d draftRow, v viewer) {
	<form
		id={draftsPage.Row |> key(d.ID)}
		class="flex items-center gap-2"
		data-url={saveDraft{} |> url(map[string]any{"rowId": d.ID})}
		data-on:submit="@post(el.dataset.url, {filterSignals: {include: /^drafts\./}})"
	>
		<label class="w-40 text-sm" for=f`draft-input-@{d.ID}`>{ d.Label }</label>
		<input id=f`draft-input-@{d.ID}` name="value" value={d.Value} data-bind=f`drafts.@{d.ID}.value` class="rounded border border-stone-300 px-2 py-1"/>
		<button class="rounded border border-stone-300 px-2 py-1 text-sm">Save</button>
		<span class="text-xs text-stone-500" data-show=js`!!$drafts[@{d.ID}].by` data-text=js`'last saved by ' + $drafts[@{d.ID}].by`>
			{ if d.By != "" { last saved by { savedBy(d, v) } } }
		</span>
		<p data-stale style="display: none" data-show=js`$drafts[@{d.ID}].stale` class="text-xs text-amber-800">
			Changed by someone else ·{" "}
			<button type="button" class="underline" data-on:click=js`const r = $drafts[@{d.ID}]; r.value = r.base = r.server; r.stale = false`>Refresh</button>
		</p>
	</form>
}

type draftsUpdates struct{}

func (draftsUpdates) ServeHTTP(w http.ResponseWriter, r *http.Request, s *store, h *hub, v viewer, d *dropper) error {
	return serveStream(w, r, s, h, v, d, func(x *push, f live.Frame) error {
		var rows []draftRow
		switch {
		case f.Topic == resync || f.Topic == "lab:draft" && f.IDs == nil:
			rows = x.store.Drafts()
		case f.Topic == "lab:draft":
			for _, id := range f.IDs {
				if d, err := x.store.Draft(id); err == nil {
					rows = append(rows, d)
				}
			}
		}
		if len(rows) == 0 {
			return nil
		}
		m := map[string]draftSignal{}
		for _, d := range rows {
			m[d.ID] = draftSignal{Server: d.Value, By: savedBy(d, x.viewer)}
		}
		x.signals(map[string]any{"drafts": m})
		return nil
	})
}

// saveDraft saves the row's bound value and answers with the row's signals.
// Its own stream hears the save too; that echo matches the value and is
// not mistaken for someone else's change.
type saveDraft struct{}

func (saveDraft) ServeHTTP(w http.ResponseWriter, r *http.Request, s *store, h *hub, v viewer) error {
	id := r.PathValue("rowId")
	var in struct {
		Drafts map[string]draftSignal `json:"drafts"`
	}
	if err := datastar.ReadSignals(r, &in); err != nil {
		return err
	}
	row, ok := in.Drafts[id]
	if !ok {
		return errNotFound
	}
	if err := s.SaveDraft(id, row.Value, string(v)); err != nil {
		return err
	}
	h.Publish(r.Context(), everyone, "lab:draft", id)
	no := false
	return datastar.NewSSE(w, r).MarshalAndPatchSignals(map[string]any{"drafts": map[string]draftSignal{
		id: {Value: row.Value, Base: row.Value, Server: row.Value, By: "you", Stale: &no},
	}})
}

component draftsPanel() {
	<SimButton action="draft" id="d1">Someone edits Revenue</SimButton>
	<SimButton action="draft" id="d2">Someone edits Operating cost</SimButton>
}
