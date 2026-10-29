package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

var errNotFound = errors.New("not found")

type series struct {
	ID     string
	Name   string
	Points int
	Status string // "ok", "stale", "failing"
}

type seriesSummary struct {
	Count, Points      int
	OK, Stale, Failing int
}

type person struct {
	ID       string
	Name     string
	Presence string // "active", "idle", "inactive"
}

type draftRow struct {
	ID    string
	Label string
	Value string
	By    string
}

type file struct {
	ID   string
	Name string
	Size int
}

type burstRow struct {
	ID    string
	Value int
}

type job struct {
	ID       string
	Name     string
	Progress int
	Status   string // "queued", "running", "done"
	Step     string
}

// store is one request's handle on the lab's shared in-memory state, which
// every tab sees. Each exported method counts as one database query.
type store struct {
	*state
	queries *atomic.Int32 // this request's queries; nil outside a request
}

type state struct {
	mu       sync.Mutex
	series   []series
	people   []person
	drafts   []draftRow
	files    []file
	burst    []burstRow
	jobs     []job
	nextFile int
}

func newStore() *store {
	s := &store{state: &state{}}
	s.reset()
	return s
}

type queriesKey struct{}

// withQueries returns ctx carrying a fresh query counter for forRequest.
func withQueries(ctx context.Context) (context.Context, *atomic.Int32) {
	n := new(atomic.Int32)
	return context.WithValue(ctx, queriesKey{}, n), n
}

// forRequest is the store handle for r, counting into r's counter.
func (s *store) forRequest(r *http.Request) (*store, error) {
	n, _ := r.Context().Value(queriesKey{}).(*atomic.Int32)
	return &store{state: s.state, queries: n}, nil
}

func (s *store) query() {
	if s.queries != nil {
		s.queries.Add(1)
	}
}

// reset restores the seed data, so each e2e test starts from the same state.
func (s *store) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.series, s.people, s.drafts, s.files, s.burst = nil, nil, nil, nil, nil
	s.nextFile = 1
	for i := 1; i <= 10; i++ {
		s.series = append(s.series, series{ID: fmt.Sprintf("s%d", i), Name: fmt.Sprintf("Well %02d pressure", i), Points: 100 * i, Status: "ok"})
	}
	for i, n := range []string{"Ada Lovelace", "Alan Turing", "Grace Hopper", "Edsger Dijkstra", "Barbara Liskov", "Ken Thompson"} {
		s.people = append(s.people, person{ID: fmt.Sprintf("p%d", i+1), Name: n, Presence: []string{"active", "idle", "inactive"}[i%3]})
	}
	for i, l := range []string{"Revenue", "Operating cost", "Headcount", "Capex", "Notes"} {
		s.drafts = append(s.drafts, draftRow{ID: fmt.Sprintf("d%d", i+1), Label: l, Value: fmt.Sprint((i + 1) * 1000)})
	}
	for _, n := range []string{"budget.xlsx", "minutes.docx", "site-photo.jpg"} {
		s.addFileLocked(n)
	}
	for i := 1; i <= 60; i++ {
		s.burst = append(s.burst, burstRow{ID: fmt.Sprintf("b%d", i), Value: i})
	}
	s.jobs = []job{
		{ID: "j1", Name: "Ingest Q3 well logs", Status: "running", Progress: 10, Step: "reading files"},
		{ID: "j2", Name: "Rebuild search index", Status: "queued", Step: "waiting"},
	}
}

func find[T any](xs []T, id string, key func(T) string) (int, error) {
	i := slices.IndexFunc(xs, func(x T) bool { return key(x) == id })
	if i < 0 {
		return -1, errNotFound
	}
	return i, nil
}

func (s *store) Series() ([]series, seriesSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	return slices.Clone(s.series), s.summaryLocked()
}

// SeriesByID loads the series with the given ids in one query; ids it
// does not know are absent from the map.
func (s *store) SeriesByID(ids []string) (map[string]series, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	out := make(map[string]series, len(ids))
	for _, x := range s.series {
		if slices.Contains(ids, x.ID) {
			out[x.ID] = x
		}
	}
	return out, nil
}

func (s *store) Summary() seriesSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	return s.summaryLocked()
}

func (s *store) summaryLocked() seriesSummary {
	var sum seriesSummary
	for _, x := range s.series {
		sum.Count++
		sum.Points += x.Points
		switch x.Status {
		case "ok":
			sum.OK++
		case "stale":
			sum.Stale++
		case "failing":
			sum.Failing++
		}
	}
	return sum
}

// TouchSeries adds points to one series and rotates its status.
func (s *store) TouchSeries(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.series, id, func(x series) string { return x.ID })
	if err != nil {
		return err
	}
	s.series[i].Points += 7
	s.series[i].Status = map[string]string{"ok": "stale", "stale": "failing", "failing": "ok"}[s.series[i].Status]
	return nil
}

func (s *store) People() []person {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	return slices.Clone(s.people)
}

func (s *store) Person(id string) (person, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.people, id, func(x person) string { return x.ID })
	if err != nil {
		return person{}, err
	}
	return s.people[i], nil
}

func (s *store) CyclePresence(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.people, id, func(x person) string { return x.ID })
	if err != nil {
		return err
	}
	s.people[i].Presence = map[string]string{"active": "idle", "idle": "inactive", "inactive": "active"}[s.people[i].Presence]
	return nil
}

func (s *store) Drafts() []draftRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	return slices.Clone(s.drafts)
}

func (s *store) Draft(id string) (draftRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.drafts, id, func(x draftRow) string { return x.ID })
	if err != nil {
		return draftRow{}, err
	}
	return s.drafts[i], nil
}

func (s *store) SaveDraft(id, value, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.drafts, id, func(x draftRow) string { return x.ID })
	if err != nil {
		return err
	}
	s.drafts[i].Value = value
	s.drafts[i].By = by
	return nil
}

func (s *store) Files() []file {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	return slices.Clone(s.files)
}

func (s *store) File(id string) (file, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.files, id, func(x file) string { return x.ID })
	if err != nil {
		return file{}, err
	}
	return s.files[i], nil
}

func (s *store) AddFile(name string) file {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	return s.addFileLocked(name)
}

func (s *store) addFileLocked(name string) file {
	f := file{ID: fmt.Sprintf("f%d", s.nextFile), Name: name, Size: 1024 * (s.nextFile*37%90 + 10)}
	s.nextFile++
	s.files = append(s.files, f)
	slices.SortFunc(s.files, func(a, b file) int { return strings.Compare(a.Name, b.Name) })
	return f
}

func (s *store) DeleteFile(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.files, id, func(x file) string { return x.ID })
	if err != nil {
		return err
	}
	s.files = slices.Delete(s.files, i, i+1)
	return nil
}

func (s *store) Burst() []burstRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	return slices.Clone(s.burst)
}

func (s *store) BurstRow(id string) (burstRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.burst, id, func(x burstRow) string { return x.ID })
	if err != nil {
		return burstRow{}, err
	}
	return s.burst[i], nil
}

func (s *store) BumpBurst(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.burst, id, func(x burstRow) string { return x.ID })
	if err != nil {
		return err
	}
	s.burst[i].Value += 100
	return nil
}

func (s *store) Jobs() []job {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	return slices.Clone(s.jobs)
}

func (s *store) Job(id string) (job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.jobs, id, func(x job) string { return x.ID })
	if err != nil {
		return job{}, err
	}
	return s.jobs[i], nil
}

// AdvanceJob moves progress by 10 points without changing the status.
func (s *store) AdvanceJob(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.jobs, id, func(x job) string { return x.ID })
	if err != nil {
		return err
	}
	s.jobs[i].Progress = min(s.jobs[i].Progress+10, 100)
	return nil
}

// StepJob moves the job to its next status, which changes the whole card.
func (s *store) StepJob(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.jobs, id, func(x job) string { return x.ID })
	if err != nil {
		return err
	}
	j := &s.jobs[i]
	switch j.Status {
	case "queued":
		j.Status, j.Step = "running", "reading files"
	case "running":
		j.Status, j.Step, j.Progress = "done", "finished", 100
	default:
		j.Status, j.Step, j.Progress = "queued", "waiting", 0
	}
	return nil
}
