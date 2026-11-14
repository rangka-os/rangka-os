package connectors

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/jobs"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Polling triggers (contract 0002 §A.1): a trigger with via: poll names one of the connector's actions. Every `every`
// (5m by default, 1m at least) the scheduler queues a "poll" job per trigger that an installed flow listens to. The job
// sends the action with the trigger's input (CEL over cursor and settings), turns the answer into events with
// for_each and emit exactly like a webhook (same scrubbing and dedupe), starts the listening flows, then stores the
// next cursor from the trigger's cursor: expression (CEL over the answer). The request is rendered at send time, so
// the action's host and secret rules apply unchanged. A connector without its keys is skipped, not dead-lettered.

// DefaultPollEvery and MinPollEvery bound a poll trigger's every.
const (
	DefaultPollEvery = 5 * time.Minute
	MinPollEvery     = time.Minute
)

// PollEvery reads a trigger's every (5m, 30s, 2h): DefaultPollEvery when missing or unreadable, at least MinPollEvery.
func PollEvery(t map[string]any) time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(meta.Str(t, "every")))
	if err != nil || d <= 0 {
		return DefaultPollEvery
	}
	if d < MinPollEvery {
		return MinPollEvery
	}
	return d
}

// PollResult is what a poll job stores.
type PollResult struct {
	Skipped string `json:"skipped,omitempty"` // needs_auth: the connector has no keys yet; sandbox: the ruang coba polls nothing
	Events  int    `json:"events"`
	Dupes   int    `json:"dupes"`
	Cursor  bool   `json:"cursor_moved"`
	Pages   int    `json:"pages,omitempty"` // pages read (next:), summed over the items
	// Truncated: a target still had a next page after MaxPollPages (audited as poll_pages_truncated)
	Truncated bool `json:"truncated,omitempty"`
	Items     int  `json:"items,omitempty"` // calls made: 1, or one per item of each:
}

// Poll runs one poll of org's connector trigger. With each: the trigger's list action is called first and the
// action is sent once per item (an outlet), with its own cursor; the item is bound under each.as for input and emit.
func (rt *Runtime) Poll(ctx context.Context, org, connector, trigger string) (*PollResult, error) {
	c, t, ok := rt.triggerVia(connector, trigger, "poll")
	if !ok {
		return nil, &jobs.Permanent{Err: fmt.Errorf("%w: no poll trigger %s.%s", ErrUnknown, connector, trigger)}
	}
	// nothing from outside reaches the ruang coba (contract 0052 §2)
	if sb, err := store.IsSandbox(ctx, rt.DB, org); err != nil {
		return nil, err
	} else if sb {
		return &PollResult{Skipped: "sandbox"}, nil
	}
	// a connector still waiting for its keys is skipped quietly: polling it every few minutes would only fill the
	// dead-letter queue and the audit trail. OAuth2 connectors refresh first, as Call does.
	if meta.Str(meta.AsMap(c["auth"]), "type") == "oauth2" {
		if err := rt.refreshIfDue(ctx, org, connector); errors.Is(err, ErrNeedsAuth) {
			return &PollResult{Skipped: "needs_auth"}, nil
		}
	}
	targets := []pollTarget{{key: trigger}}
	if each := meta.AsMap(t["each"]); each != nil {
		list := meta.Str(each, "action")
		if _, err := rt.Render(ctx, org, connector, list, nil); errors.Is(err, ErrNeedsAuth) {
			return &PollResult{Skipped: "needs_auth"}, nil
		}
		out, err := rt.Call(ctx, org, connector, list, nil)
		if err != nil {
			return nil, err
		}
		if targets, err = eachTargets(trigger, each, out); err != nil {
			return nil, &jobs.Permanent{Err: fmt.Errorf("poll %s.%s: each: %v", connector, trigger, err)}
		}
	}
	res := &PollResult{}
	for _, tg := range targets {
		r, err := rt.pollOne(ctx, org, connector, trigger, c, t, tg)
		if err != nil {
			return nil, err
		}
		if r.Skipped != "" {
			return r, nil
		}
		res.Events += r.Events
		res.Dupes += r.Dupes
		res.Cursor = res.Cursor || r.Cursor
		res.Pages += r.Pages
		res.Truncated = res.Truncated || r.Truncated
		res.Items++
	}
	return res, nil
}

// pollTarget is one call of a poll: the whole trigger, or one item of each: with its own cursor key.
type pollTarget struct {
	key  string         // rk_poll_cursors.trigger: <trigger>, or <trigger>@<item key>
	vars map[string]any // {<each.as>: item}
}

// eachTargets reads each.items (CEL over the list action's answer, a list) and each.key (CEL over the item).
func eachTargets(trigger string, each, out map[string]any) ([]pollTarget, error) {
	vars := map[string]any{"body": out}
	for k, v := range out {
		vars[k] = v
	}
	v, err := cel.Eval(meta.Str(each, "items"), varsEnv(vars))
	if err != nil {
		return nil, fmt.Errorf("items: %v", err)
	}
	as := meta.Str(each, "as")
	var targets []pollTarget
	for _, it := range meta.AsList(cel.Unwrap(v)) {
		k, err := cel.Eval(meta.Str(each, "key"), varsEnv(map[string]any{as: it}))
		if err != nil {
			return nil, fmt.Errorf("key: %v", err)
		}
		key := fmt.Sprint(cel.Unwrap(k))
		if f, ok := cel.Unwrap(k).(float64); ok && f == float64(int64(f)) {
			key = fmt.Sprint(int64(f))
		}
		if key == "" || key == "<nil>" {
			return nil, errors.New("key is empty for an item")
		}
		targets = append(targets, pollTarget{key: trigger + "@" + key, vars: map[string]any{as: it}})
	}
	return targets, nil
}

func (rt *Runtime) pollOne(ctx context.Context, org, connector, trigger string, c, t map[string]any, tg pollTarget) (*PollResult, error) {
	action := meta.Str(t, "action")
	cursor, err := rt.cursor(ctx, org, connector, tg.key)
	if err != nil {
		return nil, err
	}
	var cur any
	if cursor != "" {
		cur = cursor
	}
	// input: (CEL over cursor, settings, the each: item and page: null on the first page, then the answer's next:)
	inputFor := func(page any) (map[string]any, error) {
		vars := map[string]any{"cursor": cur, "settings": settings(c), "page": page}
		for k, v := range tg.vars {
			vars[k] = v
		}
		input := map[string]any{}
		env := varsEnv(vars)
		for k, x := range meta.AsMap(t["input"]) {
			src, ok := x.(string)
			if !ok {
				continue
			}
			v, err := cel.Eval(src, env)
			if err != nil {
				return nil, &jobs.Permanent{Err: fmt.Errorf("poll %s.%s: input %s: %v", connector, trigger, k, err)}
			}
			if u := cel.Unwrap(v); u != nil {
				input[k] = u
			}
		}
		return input, nil
	}
	input, err := inputFor(nil)
	if err != nil {
		return nil, err
	}
	if _, err := rt.Render(ctx, org, connector, action, input); errors.Is(err, ErrNeedsAuth) {
		return &PollResult{Skipped: "needs_auth"}, nil
	}
	res := &PollResult{}
	var merged map[string]any
	callCtx := ctx
	seen := map[string]bool{}
	for page := 1; ; page++ {
		out, err := rt.Call(callCtx, org, connector, action, input)
		if err != nil {
			return nil, err
		}
		ev, dupes, err := rt.dispatch(ctx, org, connector, trigger, t, out, tg.vars)
		res.Events += ev
		res.Dupes += dupes
		if err != nil {
			return nil, err
		}
		merged = mergePage(merged, out)
		res.Pages = page
		// next: (CEL over the answer) names the next page: an absolute URL (Moka's next_url) is fetched as it is, on
		// the action's host (a path such as /v3/...?page=2 is resolved against the action's URL); any other value is bound
		// as page for input:. null, "" or a value seen before stops.
		next := nextCursor(meta.Str(t, "next"), out)
		if next == "" || seen[next] {
			break
		}
		if page >= MaxPollPages {
			res.Truncated = true
			store.Audit(ctx, rt.DB, org, "connector:"+connector, "connector.error", "connector/"+connector,
				fmt.Sprintf("%s: poll stopped after %d pages", trigger, MaxPollPages), map[string]any{"trigger": trigger, "code": "poll_pages_truncated", "target": tg.key})
			break
		}
		seen[next] = true
		if strings.Contains(next, "://") || strings.HasPrefix(next, "/") {
			callCtx = context.WithValue(ctx, pageURLKey{}, next)
		} else {
			callCtx = ctx
			var pv any = next
			if n, err := strconv.ParseInt(next, 10, 64); err == nil {
				pv = n
			}
			if input, err = inputFor(pv); err != nil {
				return nil, err
			}
		}
	}
	// the cursor reads every page at once: list fields of the answers joined in page order, other fields from the
	// last page (a max() over the items, or the last page's next_cursor). A poll cut at MaxPollPages keeps the old
	// cursor: moving it past pages not read would lose them for good (newest-first vendors); the next poll reads the
	// same window again and dedupe drops what was seen
	if next := nextCursor(meta.Str(t, "cursor"), merged); next != "" && next != cursor && !res.Truncated {
		scrub, _ := rt.Scrubber(ctx, org, connector)
		if _, err := rt.DB.Exec(ctx, `insert into rk_poll_cursors (org_id, connector, trigger, cursor) values ($1, $2, $3, $4)
			on conflict (org_id, connector, trigger) do update set cursor = excluded.cursor, updated_at = now()`,
			org, connector, tg.key, scrub.String(next)); err != nil {
			return nil, fmt.Errorf("poll cursor: %w", err)
		}
		res.Cursor = true
	}
	return res, nil
}

// MaxPollPages bounds how many pages one poll of one target follows.
const MaxPollPages = 50

// mergePage joins a page's answer into the answers before it: lists are appended, other fields replaced.
func mergePage(acc, out map[string]any) map[string]any {
	if acc == nil {
		acc = map[string]any{}
	}
	for k, v := range out {
		if l, ok := v.([]any); ok {
			if prev, ok := acc[k].([]any); ok {
				joined := make([]any, 0, len(prev)+len(l))
				acc[k] = append(append(joined, prev...), l...)
				continue
			}
		}
		acc[k] = v
	}
	return acc
}

func (rt *Runtime) cursor(ctx context.Context, org, connector, trigger string) (string, error) {
	res, err := rt.DB.Query(ctx, "select cursor from rk_poll_cursors where org_id = $1 and connector = $2 and trigger = $3", org, connector, trigger)
	if err != nil {
		return "", fmt.Errorf("poll cursor: %w", err)
	}
	if len(res.Rows) == 0 {
		return "", nil
	}
	s, _ := res.Rows[0][0].(string)
	return s, nil
}

// nextCursor evaluates the trigger's cursor expression over the answer (its fields, and body); "" keeps the cursor.
func nextCursor(src string, out map[string]any) string {
	if strings.TrimSpace(src) == "" {
		return ""
	}
	vars := map[string]any{"body": out}
	for k, v := range out {
		vars[k] = v
	}
	v, err := cel.Eval(src, varsEnv(vars))
	if err != nil {
		return ""
	}
	x := cel.Unwrap(v)
	if x == nil {
		return ""
	}
	if f, ok := x.(float64); ok && f == float64(int64(f)) {
		return fmt.Sprint(int64(f))
	}
	return fmt.Sprint(x)
}

// Poll is one scheduled poll: a trigger with via: poll that an installed flow listens to.
type Poll struct {
	Connector, Trigger string
	Every              time.Duration
}

// Polls lists the poll triggers that installed flows listen to (on: <connector>.<trigger>) and that name an action,
// sorted by flow.
func Polls(reg *meta.Registry) []Poll {
	seen := map[string]bool{}
	var out []Poll
	for _, name := range sortedKeys(reg.Flows) {
		f := reg.Flows[name]
		if !reg.IsInstalled(f.App) {
			continue
		}
		conn, trig, ok := strings.Cut(f.On, ".")
		if !ok || seen[f.On] || reg.Connectors[conn] == nil {
			continue
		}
		for _, x := range meta.AsList(meta.AsMap(reg.Connectors[conn].Data)["triggers"]) {
			t := meta.AsMap(x)
			if meta.Str(t, "name") == trig && meta.Str(t, "via") == "poll" && meta.Str(t, "action") != "" {
				seen[f.On] = true
				out = append(out, Poll{Connector: conn, Trigger: trig, Every: PollEvery(t)})
			}
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
