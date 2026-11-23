package ui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/GCherry/ytmix/internal/mixer"
)

const (
	defaultMaxJobs     = 5
	defaultMaxSessions = 10
)

type sessionState struct {
	id      string
	source  string
	tempDir string
	tracks  map[string]mixer.Track
	order   []string
}

type sseEvent struct {
	name string
	data string
}

type jobStatus string

const (
	jobRunning  jobStatus = "running"
	jobComplete jobStatus = "complete"
	jobFailed   jobStatus = "failed"
)

type renderJob struct {
	id            string
	dir           string
	outPath       string
	outName       string
	tsPath        string
	tsName        string
	renderingPath string
	startedAt     time.Time
	cancel        context.CancelFunc

	mu             sync.Mutex
	status         jobStatus
	errMsg         string
	events         []sseEvent
	latestProgress *sseEvent
	subs           map[chan sseEvent]struct{}
	closed         bool
}

func newOpaqueID(prefix string) (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(raw[:]), nil
}

func newRenderJob(id, dir, outPath, outName, tsPath, tsName, renderingPath string, cancel context.CancelFunc) *renderJob {
	return &renderJob{
		id:            id,
		dir:           dir,
		outPath:       outPath,
		outName:       outName,
		tsPath:        tsPath,
		tsName:        tsName,
		renderingPath: renderingPath,
		startedAt:     time.Now(),
		cancel:        cancel,
		status:        jobRunning,
		subs:          make(map[chan sseEvent]struct{}),
	}
}

func (j *renderJob) emit(name string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	ev := sseEvent{name: name, data: string(raw)}

	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return
	}
	j.events = append(j.events, ev)
	for ch := range j.subs {
		select {
		case ch <- ev:
		default:
			// Slow subscriber buffer full; drop incremental frame rather than blocking FFmpeg pipeline.
		}
	}
}

func (j *renderJob) emitProgress(payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	ev := sseEvent{name: "progress", data: string(raw)}

	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return
	}
	j.latestProgress = &ev
	for ch := range j.subs {
		select {
		case ch <- ev:
		default:
			// Slow subscriber buffer full; drop incremental frame rather than blocking FFmpeg pipeline.
		}
	}
}

func (j *renderJob) finish(status jobStatus, errMsg string, finalEventName string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		raw = []byte(`{}`)
	}
	ev := sseEvent{name: finalEventName, data: string(raw)}

	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return
	}
	j.status = status
	j.errMsg = errMsg
	j.latestProgress = nil
	j.events = append(j.events, ev)
	for ch := range j.subs {
		select {
		case ch <- ev:
		default:
		}
		close(ch)
		delete(j.subs, ch)
	}
}

func (j *renderJob) subscribe() (history []sseEvent, ch <-chan sseEvent, done bool) {
	j.mu.Lock()
	defer j.mu.Unlock()

	history = append([]sseEvent(nil), j.events...)
	if j.status != jobRunning || j.closed {
		return history, nil, true
	}
	if j.latestProgress != nil {
		history = append(history, *j.latestProgress)
	}
	sub := make(chan sseEvent, 32)
	j.subs[sub] = struct{}{}
	return history, sub, false
}

func (j *renderJob) unsubscribe(sub <-chan sseEvent) {
	if sub == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for ch := range j.subs {
		if ch == sub {
			delete(j.subs, ch)
			close(ch)
			break
		}
	}
}

func (j *renderJob) closeAll() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return
	}
	j.closed = true
	if j.cancel != nil {
		j.cancel()
	}
	for ch := range j.subs {
		close(ch)
		delete(j.subs, ch)
	}
}

type jobStore struct {
	mu              sync.Mutex
	maxJobs         int
	maxSessions     int
	sessions        map[string]*sessionState
	sessionRing     []string
	jobs            map[string]*renderJob
	jobRing         []string
	activeJobID     string
	activeSessionID string
}

func newJobStore(maxJobs int) *jobStore {
	if maxJobs <= 0 {
		maxJobs = defaultMaxJobs
	}
	return &jobStore{
		maxJobs:     maxJobs,
		maxSessions: defaultMaxSessions,
		sessions:    make(map[string]*sessionState),
		jobs:        make(map[string]*renderJob),
	}
}

func (st *jobStore) addSession(sess *sessionState) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.sessions[sess.id] = sess
	st.sessionRing = append(st.sessionRing, sess.id)
	for len(st.sessionRing) > st.maxSessions {
		oldID := st.sessionRing[0]
		if oldID == st.activeSessionID {
			break
		}
		st.sessionRing = st.sessionRing[1:]
		if old, ok := st.sessions[oldID]; ok {
			delete(st.sessions, oldID)
			if old.tempDir != "" {
				os.RemoveAll(old.tempDir)
			}
		}
	}
}

func (st *jobStore) resolveTracks(sessionID string, trackIDs []string) ([]mixer.Track, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	sess, ok := st.sessions[sessionID]
	if !ok {
		return nil, errors.New("unknown or expired sessionId; re-probe Tracks first")
	}
	if len(trackIDs) == 0 {
		return nil, errors.New("at least one Track must be selected")
	}
	tracks := make([]mixer.Track, 0, len(trackIDs))
	for _, id := range trackIDs {
		tr, exists := sess.tracks[id]
		if !exists {
			return nil, fmt.Errorf("unknown trackId %q in session", id)
		}
		tracks = append(tracks, tr)
	}
	return tracks, nil
}

func (st *jobStore) resolveTrack(sessionID, trackID string) (mixer.Track, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	sess, ok := st.sessions[sessionID]
	if !ok {
		return mixer.Track{}, errors.New("unknown or expired sessionId; re-probe Tracks first")
	}
	tr, exists := sess.tracks[trackID]
	if !exists {
		return mixer.Track{}, fmt.Errorf("unknown trackId %q in session", trackID)
	}
	return tr, nil
}

func (st *jobStore) claimRenderSlot(job *renderJob, sessionID string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.activeJobID != "" {
		if active, ok := st.jobs[st.activeJobID]; ok {
			active.mu.Lock()
			running := active.status == jobRunning
			active.mu.Unlock()
			if running {
				return errors.New("a Mix render is already in progress")
			}
		}
	}
	st.activeJobID = job.id
	st.activeSessionID = sessionID
	st.jobs[job.id] = job
	st.jobRing = append(st.jobRing, job.id)

	for len(st.jobRing) > st.maxJobs {
		oldID := st.jobRing[0]
		if oldID == st.activeJobID {
			break
		}
		st.jobRing = st.jobRing[1:]
		if old, ok := st.jobs[oldID]; ok {
			delete(st.jobs, oldID)
			old.closeAll()
			if old.dir != "" {
				os.RemoveAll(old.dir)
			}
		}
	}
	return nil
}

func (st *jobStore) releaseActive(jobID string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.activeJobID == jobID {
		st.activeJobID = ""
		st.activeSessionID = ""
	}
}

func (st *jobStore) getJob(id string) (*renderJob, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	j, ok := st.jobs[id]
	return j, ok
}

func (st *jobStore) closeAll() {
	st.mu.Lock()
	jobs := make([]*renderJob, 0, len(st.jobs))
	for _, j := range st.jobs {
		jobs = append(jobs, j)
	}
	sessions := make([]*sessionState, 0, len(st.sessions))
	for _, s := range st.sessions {
		sessions = append(sessions, s)
	}
	st.jobs = make(map[string]*renderJob)
	st.sessions = make(map[string]*sessionState)
	st.jobRing = nil
	st.sessionRing = nil
	st.activeJobID = ""
	st.mu.Unlock()

	for _, j := range jobs {
		j.closeAll()
		if j.dir != "" {
			os.RemoveAll(j.dir)
		}
	}
	for _, s := range sessions {
		if s.tempDir != "" {
			os.RemoveAll(s.tempDir)
		}
	}
}
