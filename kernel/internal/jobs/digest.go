package jobs

import (
	"context"
	"strconv"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
)

// ExceptionDigestEvery is how often each organisation's new head-office exceptions are batched into one notice
// (contract 0066 as built: kernel.exceptions.new).
const ExceptionDigestEvery = time.Hour

// digestTick enqueues one exception_digest job per organisation for the current hour. The unique key (org + hour)
// makes it safe on several instances and across restarts, as for polls. A paused scheduler (contract 0070) skips the
// hour; the next digest's window then covers it.
func (s *Scheduler) digestTick(ctx context.Context, now time.Time, pause PauseState) {
	if s.DigestDue == nil {
		return
	}
	slot := now.Truncate(ExceptionDigestEvery)
	s.mu.Lock()
	orgs := append([]string{}, s.orgList()...)
	if s.digestLast == nil {
		s.digestLast = map[string]time.Time{}
	}
	s.mu.Unlock()
	for _, org := range orgs {
		s.mu.Lock()
		done := !slot.After(s.digestLast[org])
		s.mu.Unlock()
		if done {
			continue
		}
		if pause.skips(slot) {
			s.mu.Lock()
			s.digestLast[org] = slot
			s.mu.Unlock()
			continue
		}
		// the due check is one small query: at most once a minute per org
		s.mu.Lock()
		minute := now.Truncate(time.Minute)
		if s.digestAsked == nil {
			s.digestAsked = map[string]time.Time{}
		}
		skip := !minute.After(s.digestAsked[org])
		s.digestAsked[org] = minute
		s.mu.Unlock()
		if skip || !s.DigestDue(ctx, org, now) {
			continue
		}
		if _, err := s.Q.Enqueue(ctx, nil, "exception_digest", org, map[string]any{"slot": slot.UTC().Format(time.RFC3339)},
			engine.EnqueueOpts{MaxAttempts: 3, UniqueKey: "exception_digest:" + org + ":" + strconv.FormatInt(slot.Unix(), 10)}); err != nil {
			s.Log("scheduler: exception digest %s: %v", org, err)
			continue
		}
		s.mu.Lock()
		s.digestLast[org] = slot
		s.mu.Unlock()
	}
}

// ResetDigestAsked forgets when each org was last checked (tests).
func (s *Scheduler) ResetDigestAsked() {
	s.mu.Lock()
	s.digestAsked = nil
	s.mu.Unlock()
}
