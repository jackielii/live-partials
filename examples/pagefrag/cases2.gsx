package main

import (
	"net/http"

	"github.com/jackielii/structpages"

	"github.com/jackielii/live-partials/live"
)

// Case 3: editor with unsaved input, as fragment frames. The page declares
// that its row is pushed; the stream renders it through this page's Props
// for each viewer ("last saved by you" is per viewer), and the dispatcher
// spares a row with unsaved input exactly as for a refetch.

type draftsPage struct{}

func (draftsPage) Live() []live.Fragment {
	return []live.Fragment{{Topic: "lab:draft", Component: draftsPage.Row}}
}

// draftView is a draft row as one viewer sees it.
type draftView struct {
	draftRow
	by string
}

func asViewer(d draftRow, v viewer) draftView {
	if d.By == string(v) {
		return draftView{d, "you"}
	}
	return draftView{d, d.By}
}

func viewDraft(s *store, v viewer, id string) (draftView, error) {
	d, err := s.Draft(id)
	return asViewer(d, v), err
}

func (p draftsPage) Props(s *store, t structpages.RenderTarget, v viewer) (draftView, error) {
	if t.Is(p.Row) {
		return viewDraft(s, v, structpages.KeyOf(t))
	}
	return draftView{}, nil
}

component (p draftsPage) Page(s *store, h *hub, v viewer) {
	<Layout title="Report draft" s={s} h={h} v={v} panel={<draftsPanel/>}>
		<p class="text-sm text-stone-600">Edit a value and leave it unsaved; changes by others to that row warn instead of overwriting.</p>
		<div class="flex flex-col gap-2" data-testid="drafts">
			{ for _, d := range s.Drafts() {
				<p.Row d={asViewer(d, v)}/>
			} }
		</div>
	</Layout>
}

component (p draftsPage) Row(d draftView) {
	<form
		id={draftsPage.Row |> key(d.ID)}
		{ live.On("lab:draft", d.ID)... }
		class="flex items-center gap-2"
		hx-post={saveDraft{} |> url(map[string]any{"rowId": d.ID})}
		hx-target="this"
		hx-swap="outerMorph"
	>
		<label class="w-40 text-sm" for={"draft-input-" + d.ID}>{ d.Label }</label>
		<input id={"draft-input-" + d.ID} name="value" value={d.Value} class="rounded border border-stone-300 px-2 py-1"/>
		<button class="rounded border border-stone-300 px-2 py-1 text-sm">Save</button>
		{ if d.by != "" {
			<span class="text-xs text-stone-500">last saved by { d.by }</span>
		} }
		<p data-live-stale hidden class="text-xs text-amber-800">
			Changed by someone else ·{" "}
			<button type="button" class="underline" data-live-refresh>Refresh</button>
		</p>
	</form>
}

type saveDraft struct{}

func (saveDraft) ServeHTTP(w http.ResponseWriter, r *http.Request, s *store, h *hub, v viewer) error {
	id := r.PathValue("rowId")
	if err := s.SaveDraft(id, r.FormValue("value"), string(v)); err != nil {
		return err
	}
	h.Publish(r.Context(), everyone, "lab:draft", id)
	d, err := viewDraft(s, v, id)
	if err != nil {
		return err
	}
	return structpages.RenderComponent(draftsPage{}.Row(d))
}

component draftsPanel() {
	<SimButton action="draft" id="d1">Someone edits Revenue</SimButton>
	<SimButton action="draft" id="d2">Someone edits Operating cost</SimButton>
}

// Case 6: nested levels, as fragment frames. A progress tick pushes the bar,
// a status change pushes the card (its bar and step line inside it), both
// rendered through this page. Its middleware re-checks the viewer's access
// on every pull and every push.

type jobsPage struct{}

func (jobsPage) Live() []live.Fragment {
	return []live.Fragment{
		{Topic: "lab:job", Component: jobsPage.Card},
		{Topic: "lab:job-progress", Component: jobsPage.Progress},
	}
}

func (jobsPage) Middlewares(a *access) []structpages.MiddlewareFunc {
	return []structpages.MiddlewareFunc{a.require}
}

func (p jobsPage) Props(s *store, t structpages.RenderTarget) (job, error) {
	if t.Is(p.Card) || t.Is(p.Progress) || t.Is(p.Step) {
		return s.Job(structpages.KeyOf(t))
	}
	return job{}, nil
}

component (p jobsPage) Page(s *store, h *hub, v viewer) {
	<Layout title="Jobs" s={s} h={h} v={v} panel={<jobsPanel v={v}/>}>
		<div class="flex flex-col gap-3">
			{ for _, j := range s.Jobs() {
				<p.Card j={j}/>
			} }
		</div>
	</Layout>
}

component (p jobsPage) Card(j job) {
	<article
		id={jobsPage.Card |> key(j.ID)}
		{ live.On("lab:job", j.ID)... }
		data-testid={"job-" + j.ID}
		class="rounded border border-stone-200 bg-white p-3"
	>
		<h3 class="font-semibold">{ j.Name } <span class="text-xs text-stone-500" data-testid="job-status">{ j.Status }</span></h3>
		<p.Progress j={j}/>
		<p.Step j={j}/>
	</article>
}

component (p jobsPage) Progress(j job) {
	<div id={jobsPage.Progress |> key(j.ID)} { live.On("lab:job-progress", j.ID)... } class="my-2 h-2 rounded bg-stone-200">
		<div class="h-2 rounded bg-sky-500" style=css`width: @{j.Progress}%` data-testid="job-progress" data-progress={j.Progress}></div>
	</div>
}

component (p jobsPage) Step(j job) {
	<p id={jobsPage.Step |> key(j.ID)} { live.On("lab:job", j.ID)... } class="text-xs text-stone-500" data-testid="job-step">{ j.Step }</p>
}

component jobsPanel(v viewer) {
	<SimButton action="job-advance" id="j1">Job j1 progresses</SimButton>
	<SimButton action="job-step" id="j1">Job j1 changes status</SimButton>
	<SimButton action="revoke-jobs" id={string(v)}>Revoke my access to jobs</SimButton>
	<SimButton action="reset" id="">Reset (restores access)</SimButton>
}
