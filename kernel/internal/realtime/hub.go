// Package realtime fans out record, change set and job events to SSE clients. Events come from Postgres
// LISTEN/NOTIFY on the rk_events channel, so every kernel instance sees writes made by any instance.
package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Hub holds subscribers.
type Hub struct {
	mu        sync.Mutex
	subs      map[*sub]bool
	conns     atomic.Int64
	Heartbeat time.Duration
	listening atomic.Bool
	Log       func(format string, args ...any)
}

type sub struct {
	org    string
	models map[string]bool
	ch     chan store.Event
}

// NewHub makes a hub with a 15 s heartbeat.
func NewHub() *Hub {
	return &Hub{subs: map[*sub]bool{}, Heartbeat: 15 * time.Second, Log: func(string, ...any) {}}
}

// Connections is the number of open SSE streams.
func (h *Hub) Connections() int64 { return h.conns.Load() }

// Listening reports whether the NOTIFY listener is connected.
func (h *Hub) Listening() bool { return h.listening.Load() }

// Publish delivers an event to matching subscribers (non-blocking: slow clients drop events).
func (h *Hub) Publish(ev store.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if s.org != "" && ev.Org != "" && s.org != ev.Org {
			continue
		}
		if ev.Type == "record" && len(s.models) > 0 && !s.models[ev.Model] {
			continue
		}
		select {
		case s.ch <- ev:
		default:
		}
	}
}

// Subscribe registers a subscriber; call the returned func to leave. allow, when not nil, decides per event
// whether this subscriber may see it (the API checks record permissions there). It runs on a goroutine of
// the subscriber, not under the hub's lock, so it may query the database; events keep their order.
func (h *Hub) Subscribe(org string, models []string, allow func(store.Event) bool) (<-chan store.Event, func()) {
	s := &sub{org: org, models: map[string]bool{}, ch: make(chan store.Event, 64)}
	for _, m := range models {
		if m = strings.TrimSpace(m); m != "" {
			s.models[m] = true
		}
	}
	h.mu.Lock()
	h.subs[s] = true
	h.mu.Unlock()
	unsubscribe := func() {
		h.mu.Lock()
		delete(h.subs, s)
		h.mu.Unlock()
	}
	if allow == nil {
		return s.ch, unsubscribe
	}
	out := make(chan store.Event, 64)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case ev := <-s.ch:
				if !allow(ev) {
					continue
				}
				select {
				case out <- ev:
				case <-done:
					return
				}
			}
		}
	}()
	var once sync.Once
	return out, func() {
		once.Do(func() {
			unsubscribe()
			close(done)
		})
	}
}

// Run listens on rk_events until ctx ends, reconnecting after failures.
func (h *Hub) Run(ctx context.Context, db *pg.Pool) {
	for ctx.Err() == nil {
		l, err := db.Listen(ctx, store.EventsChannel)
		if err != nil {
			h.Log("realtime: listen: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		h.listening.Store(true)
		for {
			n, err := l.Wait(ctx)
			if err != nil {
				break
			}
			var ev store.Event
			if json.Unmarshal([]byte(n.Payload), &ev) == nil {
				h.Publish(ev)
			}
		}
		h.listening.Store(false)
		l.Close()
	}
}

// ServeSSE streams events: `event: record|changeset|job` with JSON data; `?models=deal,lead` filters
// record events; allow (nil = all) filters per event as in Subscribe; a comment line every Heartbeat keeps
// proxies from closing the stream.
func (h *Hub) ServeSSE(w http.ResponseWriter, r *http.Request, org string, allow func(store.Event) bool) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	var models []string
	if m := r.URL.Query().Get("models"); m != "" {
		models = strings.Split(m, ",")
	}
	ch, leave := h.Subscribe(org, models, allow)
	defer leave()
	h.conns.Add(1)
	defer h.conns.Add(-1)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()
	tick := time.NewTicker(h.Heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			fl.Flush()
		case ev := <-ch:
			var data any
			switch ev.Type {
			case "record":
				data = map[string]any{"model": ev.Model, "name": ev.Name, "op": ev.Op, "docstatus": ev.Docstatus}
			case "changeset":
				data = map[string]any{"id": ev.ID, "status": ev.Status}
			case "job":
				data = map[string]any{"id": ev.ID, "kind": ev.Kind, "state": ev.State}
			default:
				data = ev
			}
			b, _ := json.Marshal(data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b)
			fl.Flush()
		}
	}
}
