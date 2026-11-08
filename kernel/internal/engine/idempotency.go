package engine

import (
	"context"
	"time"
)

// Idempotent writes (contract 0030 §6): the API claims an Idempotency-Key, then the write runs with the key in its
// context; the engine marks the key committed in the same transaction as the write, so a crash between the commit and
// the stored answer still replays the record instead of writing it twice.

type idemCtxKey struct{}

// IdemKey names a claimed key.
type IdemKey struct{ Org, Actor, Key string }

// WithIdempotency returns ctx carrying a claimed key for the write that runs with it.
func WithIdempotency(ctx context.Context, k IdemKey) context.Context {
	return context.WithValue(ctx, idemCtxKey{}, k)
}

func idemFrom(ctx context.Context) (IdemKey, bool) {
	k, ok := ctx.Value(idemCtxKey{}).(IdemKey)
	return k, ok && k.Key != ""
}

type occurredCtxKey struct{}

// WithOccurredAt returns ctx carrying the moment the user pressed the button (contract 0030 §7 `_occurred_at`); the
// record events of the write keep it in their audit entries.
func WithOccurredAt(ctx context.Context, t time.Time) context.Context {
	return context.WithValue(ctx, occurredCtxKey{}, t)
}

func occurredFrom(ctx context.Context) (time.Time, bool) {
	t, ok := ctx.Value(occurredCtxKey{}).(time.Time)
	return t, ok && !t.IsZero()
}
