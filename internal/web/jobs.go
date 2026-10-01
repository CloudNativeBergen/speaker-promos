package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// A job is one export or import running in the background, so the page can
// show it progressing rather than sit on a request for minutes.
//
// The browser polls GET /jobs/{id}: while the job runs that returns its
// progress bar again, carrying the poll trigger; once it finishes it returns
// the job's own final response — the status line and whatever rows changed,
// all out-of-band — and no trigger, which is what stops the polling.
type job struct {
	id    int64
	label string

	mu       sync.Mutex
	done     int
	total    int
	step     string
	finished bool
	// final renders the response once the job has finished.
	final func(w http.ResponseWriter, r *http.Request)
}

func (j *job) progress(done, total int, step string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.done, j.total, j.step = done, total, step
}

// jobView is what job.html renders.
type jobView struct {
	ID          int64
	Label, Step string
	Done, Total int
}

func (j *job) view() jobView {
	j.mu.Lock()
	defer j.mu.Unlock()
	return jobView{ID: j.id, Label: j.label, Step: j.step, Done: j.done, Total: j.total}
}

// jobs holds the running job and the finished ones not yet collected.
//
// Only one runs at a time. Two exports into the same directory would race on
// the same files, and an import during an export would merge half-written
// bundles; refusing the second is simpler and clearer than queueing it.
type jobs struct {
	mu      sync.Mutex
	next    int64
	running *job
	byID    map[int64]*job
}

// startJob runs work in the background and answers with its progress bar.
//
// work reports progress through the job and returns the function that renders
// its final response. If another job is running, nothing starts and the status
// line says so.
func (s *Server) startJob(w http.ResponseWriter, r *http.Request, label string,
	work func(j *job) func(w http.ResponseWriter, r *http.Request)) {
	s.jobs.mu.Lock()
	if s.jobs.running != nil {
		busy := s.jobs.running.label
		s.jobs.mu.Unlock()
		s.renderTemplate(w, "status.html", statusReport{
			Summary: fmt.Sprintf("already running: %s — wait for it to finish", strings.ToLower(busy)),
			Muted:   true,
		})
		return
	}
	if s.jobs.byID == nil {
		s.jobs.byID = map[int64]*job{}
	}
	s.jobs.next++
	j := &job{id: s.jobs.next, label: label}
	s.jobs.running = j
	s.jobs.byID[j.id] = j
	s.jobs.mu.Unlock()

	go func() {
		final := work(j)
		j.mu.Lock()
		j.final, j.finished = final, true
		j.mu.Unlock()
		s.jobs.mu.Lock()
		s.jobs.running = nil
		s.jobs.mu.Unlock()
	}()

	s.renderTemplate(w, "job.html", j.view())
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.jobs.mu.Lock()
	j, ok := s.jobs.byID[id]
	s.jobs.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	j.mu.Lock()
	finished, final := j.finished, j.final
	j.mu.Unlock()
	if !finished {
		s.renderTemplate(w, "job.html", j.view())
		return
	}
	// Collected: the final response is served once.
	s.jobs.mu.Lock()
	delete(s.jobs.byID, id)
	s.jobs.mu.Unlock()
	final(w, r)
}
