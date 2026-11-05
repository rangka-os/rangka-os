package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// AIConfig points the kernel at the rangka-ai sidecar (contract 0020). A nil config, an empty URL or an empty
// CallbackURL turns the assistant off: POST /api/v1/ai/chat answers 404 and meta.features.ai is false.
type AIConfig struct {
	URL         string       // sidecar base URL (--ai-url, $RANGKA_AI_URL); the kernel posts to <URL>/v1/chat
	CallbackURL string       // kernel base URL the sidecar reaches MCP on (--ai-callback-url, else loopback + the listen port)
	Secret      string       // sent as Authorization: Bearer ($RANGKA_AI_SECRET)
	Client      *http.Client // nil = a client without its own timeout (Timeout bounds each chat)

	// Limits; zero values take the production defaults (contract 0020 §7). Tests shorten them.
	Timeout       time.Duration // one chat, sidecar included; default 120s
	RateLimit     int           // chats per person per RateWindow; default 30
	RateWindow    time.Duration // default 10 minutes
	HealthTTL     time.Duration // how long a healthy /healthz answer is trusted; default 30s
	HealthFailTTL time.Duration // how long a failed probe is trusted; default 5s
}

const (
	aiTimeout       = 120 * time.Second
	aiRateLimit     = 30
	aiRateWindow    = 10 * time.Minute
	aiTokenTTL      = 10 * time.Minute
	aiHealthTTL     = 30 * time.Second
	aiHealthFailTTL = 5 * time.Second
	aiHealthTimeout = 2 * time.Second
	aiMaxTurns      = 20
	aiMaxHistory    = 8000 // characters over all kept turns
	aiMaxMessage    = 4000 // characters in one message
)

// aiState is the per-process assistant state: who is chatting, recent chats per person, the last health probe.
type aiState struct {
	mu     sync.Mutex
	busy   map[string]bool
	starts map[string][]time.Time

	healthMu sync.Mutex
	healthAt time.Time
	healthOK bool
}

func (c *AIConfig) on() bool { return c != nil && c.URL != "" && c.CallbackURL != "" }

// aiFeature reports meta.features.ai: a sidecar is configured, config.ai.enabled is not false and the sidecar
// answers GET /healthz with 200. A healthy answer is reused for 30 seconds, a failed probe for 5. The probe does
// not inherit the request's cancellation, so a browser that leaves mid-probe cannot record the sidecar as down.
func (s *Server) aiFeature(ctx context.Context, siteEnabled bool) bool {
	if !s.AI.on() || !siteEnabled {
		return false
	}
	st := &s.ai
	st.healthMu.Lock()
	defer st.healthMu.Unlock()
	ttl := durOr(s.AI.HealthFailTTL, aiHealthFailTTL)
	if st.healthOK {
		ttl = durOr(s.AI.HealthTTL, aiHealthTTL)
	}
	if !st.healthAt.IsZero() && time.Since(st.healthAt) < ttl {
		return st.healthOK
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), aiHealthTimeout)
	defer cancel()
	ok := false
	if req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.AI.URL, "/")+"/healthz", nil); err == nil {
		if res, err := s.aiClient().Do(req); err == nil {
			io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
			res.Body.Close()
			ok = res.StatusCode == http.StatusOK
		}
	}
	st.healthAt, st.healthOK = time.Now(), ok
	return ok
}

func durOr(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

func (s *Server) aiClient() *http.Client {
	if s.AI.Client != nil {
		return s.AI.Client
	}
	return http.DefaultClient
}

// aiAcquire admits one chat for person: 429 ai_busy while another runs, 429 ai_rate_limited past the window's
// limit. A refused request does not count towards the limit. The returned func releases the chat.
func (s *Server) aiAcquire(person string) (release func(), code, msg string) {
	limit, window := s.AI.RateLimit, s.AI.RateWindow
	if limit <= 0 {
		limit = aiRateLimit
	}
	if window <= 0 {
		window = aiRateWindow
	}
	st := &s.ai
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.busy == nil {
		st.busy, st.starts = map[string]bool{}, map[string][]time.Time{}
	}
	if st.busy[person] {
		return nil, "ai_busy", "Tunggu jawaban sebelumnya selesai."
	}
	now := time.Now()
	recent := st.starts[person][:0]
	for _, t := range st.starts[person] {
		if now.Sub(t) < window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= limit {
		st.starts[person] = recent
		return nil, "ai_rate_limited", "Terlalu banyak pertanyaan untuk Raka. Coba lagi beberapa menit lagi."
	}
	st.starts[person] = append(recent, now)
	st.busy[person] = true
	return func() {
		st.mu.Lock()
		delete(st.busy, person)
		st.mu.Unlock()
	}, "", ""
}

type aiTurn struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type aiContext struct {
	Route  string `json:"route"`
	Title  string `json:"title"`
	Model  string `json:"model"`
	Record string `json:"record"`
}

type aiChatBody struct {
	Message string    `json:"message"`
	History []aiTurn  `json:"history"`
	Context aiContext `json:"context"`
}

// aiSidecarBody is what the kernel posts to <ai-url>/v1/chat (contract 0020 §2).
type aiSidecarBody struct {
	Message string    `json:"message"`
	History []aiTurn  `json:"history"`
	Context aiContext `json:"context"`
	MCP     struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	} `json:"mcp"`
	Org  string `json:"org"`
	User struct {
		ID    string   `json:"id"`
		Name  string   `json:"name"`
		Roles []string `json:"roles"`
	} `json:"user"`
}

// trimHistory keeps the last 20 turns, then drops the oldest until the kept text is at most 8,000 characters.
func trimHistory(h []aiTurn) []aiTurn {
	if len(h) > aiMaxTurns {
		h = h[len(h)-aiMaxTurns:]
	}
	total := 0
	for _, t := range h {
		total += utf8.RuneCountInString(t.Text)
	}
	for len(h) > 0 && total > aiMaxHistory {
		total -= utf8.RuneCountInString(h[0].Text)
		h = h[1:]
	}
	return append([]aiTurn{}, h...)
}

// aiChat is POST /api/v1/ai/chat: it mints an assistant token for the signed-in person and relays the chat to
// the sidecar, passing its SSE frames through unchanged. One audit entry (kind ai.chat) records the tools and
// change sets the stream named; the message text is not stored.
func (s *Server) aiChat(w http.ResponseWriter, r *http.Request, c *caller) {
	if !s.AI.on() {
		writeError(w, http.StatusNotFound, "not_found", "the AI assistant is not configured (--ai-url, --ai-callback-url)")
		return
	}
	if s.Engine.Reg().SiteConfig("ai.enabled") == false {
		writeError(w, http.StatusForbidden, "ai_disabled", "Raka dimatikan untuk organisasi ini (config.ai.enabled).")
		return
	}
	var body aiChatBody
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "body must be a JSON object: "+err.Error())
		return
	}
	if strings.TrimSpace(body.Message) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "message is required")
		return
	}
	if utf8.RuneCountInString(body.Message) > aiMaxMessage {
		writeError(w, http.StatusBadRequest, "bad_request", "Pertanyaan terlalu panjang (maksimal 4.000 karakter).")
		return
	}
	for _, t := range body.History {
		if t.Role != "user" && t.Role != "assistant" {
			writeError(w, http.StatusBadRequest, "bad_request", "history role must be user or assistant, not "+t.Role)
			return
		}
	}
	u := c.p.User
	person := u.Org + "/" + u.Email
	release, code, msg := s.aiAcquire(person)
	if release == nil {
		writeError(w, http.StatusTooManyRequests, code, msg)
		return
	}
	defer release()
	token, err := s.Auth.MintAssistantToken(r.Context(), u, aiTokenTTL)
	if err != nil {
		fail(w, err)
		return
	}
	out := aiSidecarBody{Message: body.Message, History: trimHistory(body.History), Context: body.Context, Org: u.Org}
	out.MCP.URL, out.MCP.Token = strings.TrimRight(s.AI.CallbackURL, "/")+"/mcp", token
	out.User.ID, out.User.Name, out.User.Roles = u.Email, u.FullName, u.Roles
	if out.User.Name == "" {
		out.User.Name = u.Email
	}
	if out.User.Roles == nil {
		out.User.Roles = []string{}
	}

	timeout := s.AI.Timeout
	if timeout <= 0 {
		timeout = aiTimeout
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	sse := newSSEWriter(w)
	rec := &aiRecord{}
	defer func() { s.auditChat(r, c, rec, time.Since(started)) }()
	// ends the stream with an error event, ai_timeout when the chat ran out of time
	stop := func(code, msg string) {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && r.Context().Err() == nil {
			code, msg = "ai_timeout", "Raka terlalu lama menjawab. Coba pertanyaan yang lebih singkat."
		}
		rec.outcome = code
		sse.event("error", map[string]any{"code": code, "message": msg})
	}
	const unavailable = "Raka sedang tidak bisa dihubungi. Coba lagi sebentar lagi."

	payload, _ := json.Marshal(out)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.AI.URL, "/")+"/v1/chat", bytes.NewReader(payload))
	if err != nil {
		stop("ai_unavailable", unavailable)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if s.AI.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+s.AI.Secret)
	}
	res, err := s.aiClient().Do(req)
	if err != nil {
		stop("ai_unavailable", unavailable)
		return
	}
	defer res.Body.Close()
	// a refused secret (401/403) is an operator problem: logged, and the person sees ai_unavailable
	if res.StatusCode != http.StatusOK {
		if s.Log != nil {
			s.Log("ai: sidecar answered %d to POST /v1/chat", res.StatusCode)
		}
		if res.StatusCode == http.StatusBadRequest {
			stop("bad_request", "Raka tidak bisa membaca pertanyaan ini.")
		} else {
			stop("ai_unavailable", unavailable)
		}
		return
	}
	br := bufio.NewReader(res.Body)
	for {
		frame, err := readFrame(br)
		if len(frame) > 0 {
			sse.raw(frame)
			if name := rec.note(frame); name == "done" || name == "error" {
				return
			}
		}
		if err != nil {
			if r.Context().Err() != nil {
				return // the browser left
			}
			stop("ai_unavailable", unavailable) // the sidecar ended the stream without done or error
			return
		}
	}
}

// readFrame reads one SSE frame: lines up to and including a blank line. At the end of the stream it returns
// what was read (without the blank line) and the error.
func readFrame(br *bufio.Reader) ([]byte, error) {
	var frame []byte
	for {
		line, err := br.ReadBytes('\n')
		frame = append(frame, line...)
		if err != nil {
			return frame, err
		}
		if t := bytes.TrimRight(line, "\r\n"); len(t) == 0 {
			if len(bytes.TrimSpace(frame)) == 0 {
				frame = frame[:0] // stray blank lines between frames
				continue
			}
			return frame, nil
		}
	}
}

// aiRecord collects what the audit entry keeps from the stream.
type aiRecord struct {
	tools      []string
	changesets []string
	outcome    string // done, or the error code
}

// note parses one frame and returns its event name. A frame whose data is not JSON was passed through but is
// not recorded.
func (a *aiRecord) note(frame []byte) string {
	var name string
	var data []byte
	for _, line := range strings.Split(strings.TrimRight(string(frame), "\r\n"), "\n") {
		line = strings.TrimRight(line, "\r")
		if v, ok := strings.CutPrefix(line, "event:"); ok {
			name = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(line, "data:"); ok {
			if data != nil {
				data = append(data, '\n')
			}
			data = append(data, strings.TrimPrefix(v, " ")...)
		}
	}
	var payload map[string]any
	if json.Unmarshal(data, &payload) != nil {
		return ""
	}
	switch name {
	case "tool":
		if n, _ := payload["name"].(string); n != "" {
			a.tools = append(a.tools, n)
		}
	case "changeset":
		if id, _ := payload["id"].(string); id != "" {
			a.changesets = append(a.changesets, id)
		}
	case "done":
		a.outcome = "done"
	case "error":
		a.outcome, _ = payload["code"].(string)
	}
	return name
}

// auditChat writes the ai.chat entry: person, org, tools, change sets, duration and outcome. No message text.
func (s *Server) auditChat(r *http.Request, c *caller, rec *aiRecord, took time.Duration) {
	tools, ids := rec.tools, rec.changesets
	if tools == nil {
		tools = []string{}
	}
	if ids == nil {
		ids = []string{}
	}
	outcome := rec.outcome
	if outcome == "" {
		outcome = "aborted"
	}
	data := map[string]any{"tools": tools, "changesets": ids, "duration_ms": took.Milliseconds(), "outcome": outcome}
	ctx := context.WithoutCancel(r.Context())
	if err := store.Audit(ctx, s.DB, c.actor.Org, c.actor.ID, "ai.chat", "", "Chat dengan Raka", data); err != nil && s.Log != nil {
		s.Log("ai: audit: %v", err)
	}
}

// sseWriter writes event-stream frames, sending the headers on first use and flushing after each frame.
type sseWriter struct {
	w       http.ResponseWriter
	started bool
}

func newSSEWriter(w http.ResponseWriter) *sseWriter { return &sseWriter{w: w} }

func (s *sseWriter) raw(frame []byte) {
	if !s.started {
		h := s.w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Accel-Buffering", "no")
		s.w.WriteHeader(http.StatusOK)
		s.started = true
	}
	s.w.Write(frame)
	// a last frame cut off by the end of the stream still gets its blank line
	switch {
	case bytes.HasSuffix(frame, []byte("\n\n")), bytes.HasSuffix(frame, []byte("\r\n\r\n")):
	case bytes.HasSuffix(frame, []byte("\n")):
		s.w.Write([]byte("\n"))
	default:
		s.w.Write([]byte("\n\n"))
	}
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *sseWriter) event(name string, data any) {
	b, _ := json.Marshal(data)
	s.raw([]byte("event: " + name + "\ndata: " + string(b) + "\n\n"))
}
