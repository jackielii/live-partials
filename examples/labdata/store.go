package labdata

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

var ErrNotFound = errors.New("not found")

type Series struct {
	ID     string
	Name   string
	Points int
	Status string // "ok", "stale", "failing"
}

type SeriesSummary struct {
	Count, Points      int
	OK, Stale, Failing int
}

type Person struct {
	ID       string
	Name     string
	Presence string // "active", "idle", "inactive"
}

type DraftRow struct {
	ID    string
	Label string
	Value string
	By    string
}

type File struct {
	ID   string
	Name string
	Size int
}

type BurstRow struct {
	ID    string
	Value int
}

type Job struct {
	ID       string
	Name     string
	Progress int
	Status   string // "queued", "running", "done"
	Step     string
}

// Store is one request's handle on the lab's shared in-memory state, which
// every tab sees. Each exported method counts as one database query.
type Store struct {
	*state
	queries *atomic.Int32 // this handle's queries; nil outside a request
}

type state struct {
	mu       sync.Mutex
	total    atomic.Int64 // every query through any handle, for the fan-out measurement
	series   []Series
	people   []Person
	crowd    []Person
	drafts   []DraftRow
	files    []File
	burst    []BurstRow
	jobs     []Job
	nextFile int
}

func New() *Store {
	s := &Store{state: &state{}}
	s.Reset()
	return s
}

type queriesKey struct{}

// WithQueries returns ctx carrying a fresh query counter for ForRequest.
func WithQueries(ctx context.Context) (context.Context, *atomic.Int32) {
	n := new(atomic.Int32)
	return context.WithValue(ctx, queriesKey{}, n), n
}

// ForRequest is the store handle for r, counting into r's counter.
func (s *Store) ForRequest(r *http.Request) (*Store, error) {
	return s.ForContext(r.Context()), nil
}

// ForContext is the store handle counting into ctx's counter (WithQueries).
func (s *Store) ForContext(ctx context.Context) *Store {
	n, _ := ctx.Value(queriesKey{}).(*atomic.Int32)
	return &Store{state: s.state, queries: n}
}

// Counting is a handle whose queries are counted into n.
func (s *Store) Counting(n *atomic.Int32) *Store {
	return &Store{state: s.state, queries: n}
}

// TotalQueries is the number of queries made through every handle.
func (s *Store) TotalQueries() int64 { return s.total.Load() }

func (s *Store) query() {
	s.total.Add(1)
	if s.queries != nil {
		s.queries.Add(1)
	}
}

// Reset restores the seed data, so each e2e test starts from the same state.
func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.series, s.people, s.crowd, s.drafts, s.files, s.burst = nil, nil, nil, nil, nil, nil
	s.nextFile = 1
	for i := 1; i <= 10; i++ {
		s.series = append(s.series, Series{ID: fmt.Sprintf("s%d", i), Name: fmt.Sprintf("Well %02d pressure", i), Points: 100 * i, Status: "ok"})
	}
	for i, n := range []string{"Ada Lovelace", "Alan Turing", "Grace Hopper", "Edsger Dijkstra", "Barbara Liskov", "Ken Thompson"} {
		s.people = append(s.people, Person{ID: fmt.Sprintf("p%d", i+1), Name: n, Presence: []string{"active", "idle", "inactive"}[i%3]})
	}
	for i := 1; i <= 50; i++ {
		s.crowd = append(s.crowd, Person{ID: fmt.Sprintf("c%d", i), Name: fmt.Sprintf("Member %02d", i), Presence: []string{"active", "idle", "inactive"}[i%3]})
	}
	for i, l := range []string{"Revenue", "Operating cost", "Headcount", "Capex", "Notes"} {
		s.drafts = append(s.drafts, DraftRow{ID: fmt.Sprintf("d%d", i+1), Label: l, Value: fmt.Sprint((i + 1) * 1000)})
	}
	for _, n := range []string{"budget.xlsx", "minutes.docx", "site-photo.jpg"} {
		s.addFileLocked(n)
	}
	for i := 1; i <= 60; i++ {
		s.burst = append(s.burst, BurstRow{ID: fmt.Sprintf("b%d", i), Value: i})
	}
	s.jobs = []Job{
		{ID: "j1", Name: "Ingest Q3 well logs", Status: "running", Progress: 10, Step: "reading files"},
		{ID: "j2", Name: "Rebuild search index", Status: "queued", Step: "waiting"},
	}
}

func find[T any](xs []T, id string, key func(T) string) (int, error) {
	i := slices.IndexFunc(xs, func(x T) bool { return key(x) == id })
	if i < 0 {
		return -1, ErrNotFound
	}
	return i, nil
}

func (s *Store) Series() ([]Series, SeriesSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	return slices.Clone(s.series), s.summaryLocked()
}

// SeriesByID loads the series with the given ids in one query; ids it
// does not know are absent from the map.
func (s *Store) SeriesByID(ids []string) (map[string]Series, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	out := make(map[string]Series, len(ids))
	for _, x := range s.series {
		if slices.Contains(ids, x.ID) {
			out[x.ID] = x
		}
	}
	return out, nil
}

func (s *Store) Summary() SeriesSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	return s.summaryLocked()
}

func (s *Store) summaryLocked() SeriesSummary {
	var sum SeriesSummary
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
func (s *Store) TouchSeries(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.series, id, func(x Series) string { return x.ID })
	if err != nil {
		return err
	}
	s.series[i].Points += 7
	s.series[i].Status = map[string]string{"ok": "stale", "stale": "failing", "failing": "ok"}[s.series[i].Status]
	return nil
}

func (s *Store) People() []Person {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	return slices.Clone(s.people)
}

// Crowd is fifty more people, shown only by the hybrid stage's case 2 to
// put many presence dots on one page.
func (s *Store) Crowd() []Person {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	return slices.Clone(s.crowd)
}

func (s *Store) Person(id string) (Person, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	p, err := s.personLocked(id)
	if err != nil {
		return Person{}, err
	}
	return *p, nil
}

// Presence returns the presence of each known id in one query.
func (s *Store) Presence(ids []string) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		if p, err := s.personLocked(id); err == nil {
			out[id] = p.Presence
		}
	}
	return out
}

// AllPresence returns everyone's presence, people and crowd, in one query.
func (s *Store) AllPresence() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	out := make(map[string]string, len(s.people)+len(s.crowd))
	for _, p := range slices.Concat(s.people, s.crowd) {
		out[p.ID] = p.Presence
	}
	return out
}

func (s *Store) personLocked(id string) (*Person, error) {
	for _, xs := range [][]Person{s.people, s.crowd} {
		if i, err := find(xs, id, func(x Person) string { return x.ID }); err == nil {
			return &xs[i], nil
		}
	}
	return nil, ErrNotFound
}

func (s *Store) CyclePresence(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	p, err := s.personLocked(id)
	if err != nil {
		return err
	}
	p.Presence = map[string]string{"active": "idle", "idle": "inactive", "inactive": "active"}[p.Presence]
	return nil
}

// CycleCrowd moves every crowd member to their next presence in one query
// and returns their ids.
func (s *Store) CycleCrowd() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	ids := make([]string, len(s.crowd))
	for i := range s.crowd {
		p := &s.crowd[i]
		p.Presence = map[string]string{"active": "idle", "idle": "inactive", "inactive": "active"}[p.Presence]
		ids[i] = p.ID
	}
	return ids
}

func (s *Store) Drafts() []DraftRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	return slices.Clone(s.drafts)
}

func (s *Store) Draft(id string) (DraftRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.drafts, id, func(x DraftRow) string { return x.ID })
	if err != nil {
		return DraftRow{}, err
	}
	return s.drafts[i], nil
}

func (s *Store) SaveDraft(id, value, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.drafts, id, func(x DraftRow) string { return x.ID })
	if err != nil {
		return err
	}
	s.drafts[i].Value = value
	s.drafts[i].By = by
	return nil
}

func (s *Store) Files() []File {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	return slices.Clone(s.files)
}

func (s *Store) File(id string) (File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.files, id, func(x File) string { return x.ID })
	if err != nil {
		return File{}, err
	}
	return s.files[i], nil
}

func (s *Store) AddFile(name string) File {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	return s.addFileLocked(name)
}

func (s *Store) addFileLocked(name string) File {
	f := File{ID: fmt.Sprintf("f%d", s.nextFile), Name: name, Size: 1024 * (s.nextFile*37%90 + 10)}
	s.nextFile++
	s.files = append(s.files, f)
	slices.SortFunc(s.files, func(a, b File) int { return strings.Compare(a.Name, b.Name) })
	return f
}

func (s *Store) DeleteFile(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.files, id, func(x File) string { return x.ID })
	if err != nil {
		return err
	}
	s.files = slices.Delete(s.files, i, i+1)
	return nil
}

func (s *Store) Burst() []BurstRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	return slices.Clone(s.burst)
}

func (s *Store) BurstRow(id string) (BurstRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.burst, id, func(x BurstRow) string { return x.ID })
	if err != nil {
		return BurstRow{}, err
	}
	return s.burst[i], nil
}

func (s *Store) BumpBurst(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.burst, id, func(x BurstRow) string { return x.ID })
	if err != nil {
		return err
	}
	s.burst[i].Value += 100
	return nil
}

func (s *Store) Jobs() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	return slices.Clone(s.jobs)
}

func (s *Store) Job(id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.jobs, id, func(x Job) string { return x.ID })
	if err != nil {
		return Job{}, err
	}
	return s.jobs[i], nil
}

// AdvanceJob moves progress by 10 points without changing the status.
func (s *Store) AdvanceJob(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.jobs, id, func(x Job) string { return x.ID })
	if err != nil {
		return err
	}
	s.jobs[i].Progress = min(s.jobs[i].Progress+10, 100)
	return nil
}

// StepJob moves the job to its next status, which changes the whole card.
func (s *Store) StepJob(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	i, err := find(s.jobs, id, func(x Job) string { return x.ID })
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

// Catalog returns the series whose status is status, or every series when
// status is empty, in catalog order.
func (s *Store) Catalog(status string) []Series {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query()
	var out []Series
	for _, x := range s.series {
		if status == "" || x.Status == status {
			out = append(out, x)
		}
	}
	return out
}
