// Package ratelimit is the kernel's traffic shaping (contract 0040 §9): token buckets that delay requests over the
// plan's rate instead of refusing them, and refuse only when the wait would be too long or too many requests
// already wait. Buckets live in process memory: under `rangka serve` with one process that is the whole tenant;
// under Lambda each instance has its own, so they are a backstop behind the hosting router's shaping.
package ratelimit

import (
	"context"
	"math"
	"sync"
	"time"
)

// Rate is a token bucket: RPS tokens a second, at most Burst; a request without a token waits up to MaxWait, with
// at most MaxWaiting requests waiting at once.
type Rate struct {
	RPS        float64
	Burst      int
	MaxWait    time.Duration
	MaxWaiting int
}

// Refused is the answer when a request cannot be served within the rate.
type Refused struct{ RetryAfter time.Duration }

func (r *Refused) Error() string { return "ratelimit: over the plan's rate" }

// RetrySeconds is RetryAfter in whole seconds, at least 1, for a Retry-After header.
func (r *Refused) RetrySeconds() int {
	if s := int(math.Ceil(r.RetryAfter.Seconds())); s > 1 {
		return s
	}
	return 1
}

// Limiter holds one bucket per key and counts the day's waits and refusals. The zero value is ready to use.
type Limiter struct {
	Now func() time.Time
	// MaxKeys bounds the per-key buckets (one per client IP for unauthenticated routes); past it, idle buckets
	// are dropped. 0 means 10000.
	MaxKeys int

	mu        sync.Mutex
	buckets   map[string]*bucket
	day       string
	waitedMS  int64
	refused   int64
	throttled int64
}

type bucket struct {
	rate    Rate
	tokens  float64
	last    time.Time
	waiting int
}

func (l *Limiter) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Wait takes a token of key under rate, waiting for one when none is free. It returns how long it waited, a
// *Refused when the wait would pass rate.MaxWait or rate.MaxWaiting requests already wait, or ctx's error when the
// request went away while waiting. A zero rate never waits.
func (l *Limiter) Wait(ctx context.Context, key string, rate Rate) (time.Duration, error) {
	if rate.RPS <= 0 {
		return 0, nil
	}
	burst := math.Max(1, float64(rate.Burst))
	l.mu.Lock()
	now := l.now()
	l.rollDay(now)
	if l.buckets == nil {
		l.buckets = map[string]*bucket{}
	}
	b, ok := l.buckets[key]
	if !ok || b.rate != rate {
		l.prune(now)
		b = &bucket{rate: rate, tokens: burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(burst, b.tokens+now.Sub(b.last).Seconds()*rate.RPS)
	b.last = now
	b.tokens-- // reserve: a negative balance is the queue in front of this request
	if b.tokens >= 0 {
		l.mu.Unlock()
		return 0, nil
	}
	wait := time.Duration(-b.tokens / rate.RPS * float64(time.Second))
	if wait > rate.MaxWait || b.waiting >= rate.MaxWaiting {
		b.tokens++
		l.refused++
		l.mu.Unlock()
		return 0, &Refused{RetryAfter: wait}
	}
	b.waiting++
	l.mu.Unlock()

	t := time.NewTimer(wait)
	defer t.Stop()
	var err error
	select {
	case <-t.C:
	case <-ctx.Done():
		err = ctx.Err()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b.waiting--
	if err != nil {
		b.tokens++ // the request went away: its place goes to the next one
		return 0, err
	}
	l.rollDay(l.now())
	l.throttled++
	l.waitedMS += wait.Milliseconds()
	return wait, nil
}

// prune drops buckets that are full again and have nobody waiting once there are MaxKeys of them. l.mu is held.
func (l *Limiter) prune(now time.Time) {
	max := l.MaxKeys
	if max <= 0 {
		max = 10000
	}
	if len(l.buckets) < max {
		return
	}
	for k, b := range l.buckets {
		if b.waiting == 0 && b.tokens+now.Sub(b.last).Seconds()*b.rate.RPS >= float64(b.rate.Burst) {
			delete(l.buckets, k)
		}
	}
}

func (l *Limiter) rollDay(now time.Time) {
	if d := now.Format("2006-01-02"); d != l.day {
		l.day, l.waitedMS, l.refused, l.throttled = d, 0, 0, 0
	}
}

// Today is what this process shaped today (its local date): requests that waited, their total wait and
// refusals. In-process counters, so approximate under several instances.
func (l *Limiter) Today() (throttled, waitedMS, refused int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rollDay(l.now())
	return l.throttled, l.waitedMS, l.refused
}
