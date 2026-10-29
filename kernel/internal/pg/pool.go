package pg

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Querier is what the store needs: a pool, a connection or a transaction.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (*Result, error)
	Exec(ctx context.Context, sql string, args ...any) (*Result, error)
}

// Pool is a fixed-size connection pool.
type Pool struct {
	cfg    Config
	idle   chan *Conn
	slots  chan struct{}
	closed atomic.Bool
	mu     sync.Mutex
	all    map[*Conn]bool
}

// NewPool connects lazily; it checks the first connection.
func NewPool(ctx context.Context, url string, size int) (*Pool, error) {
	cfg, err := ParseURL(url)
	if err != nil {
		return nil, err
	}
	if size <= 0 {
		size = 10
	}
	p := &Pool{cfg: cfg, idle: make(chan *Conn, size), slots: make(chan struct{}, size), all: map[*Conn]bool{}}
	c, err := p.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	p.Release(c)
	return p, nil
}

// Config returns the connection settings.
func (p *Pool) Config() Config { return p.cfg }

// Acquire takes a connection (opening one when the pool has room).
func (p *Pool) Acquire(ctx context.Context) (*Conn, error) {
	if p.closed.Load() {
		return nil, errors.New("pg: pool closed")
	}
	for {
		select {
		case c := <-p.idle:
			if c.Broken() {
				p.forget(c)
				continue
			}
			return c, nil
		default:
		}
		select {
		case c := <-p.idle:
			if c.Broken() {
				p.forget(c)
				continue
			}
			return c, nil
		case p.slots <- struct{}{}:
			c, err := Connect(ctx, p.cfg)
			if err != nil {
				<-p.slots
				return nil, err
			}
			p.mu.Lock()
			p.all[c] = true
			p.mu.Unlock()
			return c, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (p *Pool) forget(c *Conn) {
	c.Close()
	p.mu.Lock()
	delete(p.all, c)
	p.mu.Unlock()
	<-p.slots
}

// Release returns a connection; broken connections and ones left inside a transaction are closed.
func (p *Pool) Release(c *Conn) {
	if c == nil {
		return
	}
	if c.Broken() || c.TxStatus() != 'I' || p.closed.Load() {
		p.forget(c)
		return
	}
	p.idle <- c
}

// Query runs a statement on a pooled connection.
func (p *Pool) Query(ctx context.Context, sql string, args ...any) (*Result, error) {
	c, err := p.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer p.Release(c)
	return c.Query(ctx, sql, args...)
}

// Exec runs a statement (or, without args, several statements) on a pooled connection.
func (p *Pool) Exec(ctx context.Context, sql string, args ...any) (*Result, error) {
	c, err := p.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer p.Release(c)
	return c.Exec(ctx, sql, args...)
}

// Ping measures a round trip.
func (p *Pool) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	_, err := p.Query(ctx, "select 1")
	return time.Since(start), err
}

// Stats returns open and idle connection counts.
func (p *Pool) Stats() (open, idle int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.all), len(p.idle)
}

// Close closes idle connections and refuses new work.
func (p *Pool) Close() {
	p.closed.Store(true)
	for {
		select {
		case c := <-p.idle:
			p.forget(c)
		default:
			return
		}
	}
}

// Tx is a transaction on one connection.
type Tx struct {
	c    *Conn
	p    *Pool
	done bool
	// AfterCommit callbacks run once the transaction committed.
	after []func()
}

// Begin starts a transaction.
func (p *Pool) Begin(ctx context.Context) (*Tx, error) {
	c, err := p.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := c.Simple(ctx, "BEGIN"); err != nil {
		p.Release(c)
		return nil, err
	}
	return &Tx{c: c, p: p}, nil
}

func (t *Tx) Query(ctx context.Context, sql string, args ...any) (*Result, error) {
	if t.done {
		return nil, errors.New("pg: transaction is finished")
	}
	return t.c.Query(ctx, sql, args...)
}

func (t *Tx) Exec(ctx context.Context, sql string, args ...any) (*Result, error) {
	if t.done {
		return nil, errors.New("pg: transaction is finished")
	}
	return t.c.Exec(ctx, sql, args...)
}

// AfterCommit registers fn to run after a successful commit.
func (t *Tx) AfterCommit(fn func()) { t.after = append(t.after, fn) }

// Commit commits and releases the connection.
func (t *Tx) Commit(ctx context.Context) error {
	if t.done {
		return errors.New("pg: transaction is finished")
	}
	t.done = true
	_, err := t.c.Simple(ctx, "COMMIT")
	t.p.Release(t.c)
	if err == nil {
		for _, fn := range t.after {
			fn()
		}
	}
	return err
}

// Rollback aborts (a no-op after Commit) and releases the connection.
func (t *Tx) Rollback(ctx context.Context) error {
	if t.done {
		return nil
	}
	t.done = true
	_, err := t.c.Simple(context.WithoutCancel(ctx), "ROLLBACK")
	t.p.Release(t.c)
	return err
}

// Listener holds a dedicated connection for LISTEN.
type Listener struct {
	c *Conn
}

// Listen opens a dedicated connection and listens on channels.
func (p *Pool) Listen(ctx context.Context, channels ...string) (*Listener, error) {
	c, err := Connect(ctx, p.cfg)
	if err != nil {
		return nil, err
	}
	for _, ch := range channels {
		if _, err := c.Simple(ctx, "LISTEN "+QuoteIdent(ch)); err != nil {
			c.Close()
			return nil, err
		}
	}
	return &Listener{c: c}, nil
}

// Wait returns the next notification.
func (l *Listener) Wait(ctx context.Context) (*Notification, error) { return l.c.WaitNotification(ctx) }

// Broken reports whether the listener must be reopened.
func (l *Listener) Broken() bool { return l.c.Broken() }

// Close closes the listener connection.
func (l *Listener) Close() error { return l.c.Close() }
