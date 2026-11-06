// Package pg is the kernel's PostgreSQL access layer on pgx v5: a pool, transactions, COPY and
// LISTEN/NOTIFY, with results buffered and decoded into plain Go values (see normalize).
package pg

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier is what the store needs: a pool or a transaction.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (*Result, error)
	Exec(ctx context.Context, sql string, args ...any) (*Result, error)
}

// Error is a server error (ErrorResponse).
type Error struct {
	Severity, Code, Message, Detail, Hint, Position, Table, Column, Constraint string
	pe                                                                         *pgconn.PgError
}

func (e *Error) Error() string {
	s := "pg: " + e.Message + " (SQLSTATE " + e.Code + ")"
	if e.Detail != "" {
		s += ": " + e.Detail
	}
	return s
}

// Unwrap returns the pgconn error.
func (e *Error) Unwrap() error { return e.pe }

// IsCode reports whether err is a server error with SQLSTATE code (e.g. 23505 unique_violation).
func IsCode(err error, code string) bool {
	var pe *Error
	return errors.As(err, &pe) && pe.Code == code
}

// wrapErr turns a *pgconn.PgError into an *Error.
func wrapErr(err error) error {
	var pe *pgconn.PgError
	if err == nil || !errors.As(err, &pe) {
		return err
	}
	pos := ""
	if pe.Position != 0 {
		pos = strconv.Itoa(int(pe.Position))
	}
	return &Error{Severity: pe.Severity, Code: pe.Code, Message: pe.Message, Detail: pe.Detail, Hint: pe.Hint, Position: pos,
		Table: pe.TableName, Column: pe.ColumnName, Constraint: pe.ConstraintName, pe: pe}
}

// Notification is a NOTIFY message.
type Notification struct {
	PID     uint32
	Channel string
	Payload string
}

// Result is a fully read query result.
type Result struct {
	Columns []string
	OIDs    []uint32
	Rows    [][]any
	Tag     string
}

// Maps returns the rows as column → value maps.
func (r *Result) Maps() []map[string]any {
	out := make([]map[string]any, len(r.Rows))
	for i, row := range r.Rows {
		m := make(map[string]any, len(r.Columns))
		for j, c := range r.Columns {
			m[c] = row[j]
		}
		out[i] = m
	}
	return out
}

// RowsAffected parses the command tag ("UPDATE 3" → 3).
func (r *Result) RowsAffected() int64 {
	f := strings.Fields(r.Tag)
	if len(f) == 0 {
		return 0
	}
	n, _ := strconv.ParseInt(f[len(f)-1], 10, 64)
	return n
}

// conn is what pgxpool.Pool, pgx.Conn and pgx.Tx share.
type conn interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// query runs one statement through the extended protocol and buffers the rows.
func query(ctx context.Context, c conn, sql string, args []any) (*Result, error) {
	args, err := encodeArgs(args)
	if err != nil {
		return nil, err
	}
	rows, err := c.Query(ctx, sql, args...)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()
	fds := rows.FieldDescriptions()
	res := &Result{Columns: make([]string, len(fds)), OIDs: make([]uint32, len(fds))}
	for i, fd := range fds {
		res.Columns[i], res.OIDs[i] = fd.Name, fd.DataTypeOID
	}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, wrapErr(err)
		}
		for i, v := range vals {
			vals[i] = normalize(res.OIDs[i], v)
		}
		res.Rows = append(res.Rows, vals)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapErr(err)
	}
	res.Tag = rows.CommandTag().String()
	return res, nil
}

// exec runs a statement; without args it may hold several statements (simple protocol).
func exec(ctx context.Context, c conn, sql string, args []any) (*Result, error) {
	if len(args) > 0 {
		return query(ctx, c, sql, args)
	}
	tag, err := c.Exec(ctx, sql)
	if err != nil {
		return nil, wrapErr(err)
	}
	return &Result{Tag: tag.String()}, nil
}

// Pool is a connection pool.
type Pool struct {
	p    *pgxpool.Pool
	orgs sync.Map // *pgx.Conn → rangka.org as last set on it (see prepareConn)
}

// NewPool opens a pool of at most size connections and checks one.
func NewPool(ctx context.Context, url string, size int) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	if size <= 0 {
		size = 10
	}
	cfg.MaxConns = int32(size)
	// Unnamed statements, no statement cache: a cached plan fails once after DDL changes a table
	// ("cached plan must not change result type"), and change sets alter tables at runtime.
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	cfg.AfterConnect = func(_ context.Context, c *pgx.Conn) error {
		registerJSON(c.TypeMap())
		return nil
	}
	pool := &Pool{}
	// Every pooled connection (plain queries, COPY and transactions alike) runs with rangka.org set from
	// OrgFrom(ctx) before it is handed out; the listener's dedicated connection is exempt (contract 0010 §4).
	cfg.PrepareConn = pool.prepareConn
	cfg.BeforeClose = func(c *pgx.Conn) { pool.orgs.Delete(c) }
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := p.Ping(ctx); err != nil {
		p.Close()
		return nil, wrapErr(err)
	}
	pool.p = p
	return pool, nil
}

// registerJSON makes json and jsonb decode through DecodeJSON (integral numbers as int64).
func registerJSON(m *pgtype.Map) {
	m.RegisterType(&pgtype.Type{Name: "json", OID: pgtype.JSONOID, Codec: &pgtype.JSONCodec{Marshal: json.Marshal, Unmarshal: unmarshalJSON}})
	m.RegisterType(&pgtype.Type{Name: "jsonb", OID: pgtype.JSONBOID, Codec: &pgtype.JSONBCodec{Marshal: json.Marshal, Unmarshal: unmarshalJSON}})
}

func unmarshalJSON(b []byte, v any) error {
	if p, ok := v.(*any); ok {
		*p = DecodeJSON(b)
		return nil
	}
	return json.Unmarshal(b, v)
}

// Config returns the pool settings.
func (p *Pool) Config() *pgxpool.Config { return p.p.Config() }

// Query runs a statement on a pooled connection.
func (p *Pool) Query(ctx context.Context, sql string, args ...any) (*Result, error) {
	return query(ctx, p.p, sql, args)
}

// Exec runs a statement (or, without args, several statements) on a pooled connection.
func (p *Pool) Exec(ctx context.Context, sql string, args ...any) (*Result, error) {
	return exec(ctx, p.p, sql, args)
}

// CopyTo runs COPY … TO STDOUT into w and returns the row count.
func (p *Pool) CopyTo(ctx context.Context, w io.Writer, sql string) (int64, error) {
	c, err := p.p.Acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer c.Release()
	tag, err := c.Conn().PgConn().CopyTo(ctx, w, sql)
	if err != nil {
		return 0, wrapErr(err)
	}
	return tag.RowsAffected(), nil
}

// Ping measures a round trip.
func (p *Pool) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	_, err := p.Query(ctx, "select 1")
	return time.Since(start), err
}

// Stats returns open and idle connection counts.
func (p *Pool) Stats() (open, idle int) {
	s := p.p.Stat()
	return int(s.TotalConns()), int(s.IdleConns())
}

// Close closes the pool; it waits for connections in use to be released.
func (p *Pool) Close() { p.p.Close() }

// Tx is a transaction on one connection.
type Tx struct {
	tx   pgx.Tx
	done bool
	// AfterCommit callbacks run once the transaction committed.
	after []func()
	// conflict is set when a statement or the commit failed with a serialization failure or a deadlock: the
	// transaction lost a race and the whole unit of work may be run again (see Conflicted).
	conflict bool
}

// note records a serialization failure (40001) or deadlock (40P01) and returns err unchanged.
func (t *Tx) note(err error) error {
	if IsCode(err, "40001") || IsCode(err, "40P01") {
		t.conflict = true
	}
	return err
}

// Conflicted reports whether the transaction failed because a concurrent transaction won (SQLSTATE 40001 or
// 40P01), even when the caller wrapped or replaced that error: running the unit of work again is safe.
func (t *Tx) Conflicted() bool { return t.conflict }

var errTxDone = errors.New("pg: transaction is finished")

// Begin starts a transaction.
func (p *Pool) Begin(ctx context.Context) (*Tx, error) {
	tx, err := p.p.Begin(ctx)
	if err != nil {
		return nil, wrapErr(err)
	}
	return &Tx{tx: tx}, nil
}

// BeginSerializable starts a transaction at isolation level SERIALIZABLE: it commits only if running it alone
// would have given the same result, else a statement or the commit fails with SQLSTATE 40001 (Conflicted).
func (p *Pool) BeginSerializable(ctx context.Context) (*Tx, error) {
	tx, err := p.p.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, wrapErr(err)
	}
	return &Tx{tx: tx}, nil
}

func (t *Tx) Query(ctx context.Context, sql string, args ...any) (*Result, error) {
	if t.done {
		return nil, errTxDone
	}
	res, err := query(ctx, t.tx, sql, args)
	return res, t.note(err)
}

func (t *Tx) Exec(ctx context.Context, sql string, args ...any) (*Result, error) {
	if t.done {
		return nil, errTxDone
	}
	res, err := exec(ctx, t.tx, sql, args)
	return res, t.note(err)
}

// CopyFrom runs COPY … FROM STDIN with r as input and returns the row count.
func (t *Tx) CopyFrom(ctx context.Context, r io.Reader, sql string) (int64, error) {
	if t.done {
		return 0, errTxDone
	}
	tag, err := t.tx.Conn().PgConn().CopyFrom(ctx, r, sql)
	if err != nil {
		return 0, t.note(wrapErr(err))
	}
	return tag.RowsAffected(), nil
}

// CopyTo runs COPY … TO STDOUT into w on the transaction's connection (so it sees the transaction's snapshot
// and SET LOCAL settings) and returns the row count.
func (t *Tx) CopyTo(ctx context.Context, w io.Writer, sql string) (int64, error) {
	if t.done {
		return 0, errTxDone
	}
	tag, err := t.tx.Conn().PgConn().CopyTo(ctx, w, sql)
	if err != nil {
		return 0, wrapErr(err)
	}
	return tag.RowsAffected(), nil
}

// AfterCommit registers fn to run after a successful commit.
func (t *Tx) AfterCommit(fn func()) { t.after = append(t.after, fn) }

// Commit commits and releases the connection.
func (t *Tx) Commit(ctx context.Context) error {
	if t.done {
		return errTxDone
	}
	t.done = true
	if err := t.tx.Commit(ctx); err != nil {
		return t.note(wrapErr(err))
	}
	for _, fn := range t.after {
		fn()
	}
	return nil
}

// Rollback aborts (a no-op after Commit) and releases the connection.
func (t *Tx) Rollback(ctx context.Context) error {
	if t.done {
		return nil
	}
	t.done = true
	return wrapErr(t.tx.Rollback(context.WithoutCancel(ctx)))
}

// Listener holds a dedicated connection for LISTEN.
type Listener struct {
	c *pgx.Conn
}

// Listen opens a dedicated connection and listens on channels.
func (p *Pool) Listen(ctx context.Context, channels ...string) (*Listener, error) {
	c, err := pgx.ConnectConfig(ctx, p.p.Config().ConnConfig.Copy())
	if err != nil {
		return nil, wrapErr(err)
	}
	for _, ch := range channels {
		if _, err := c.Exec(ctx, "LISTEN "+QuoteIdent(ch)); err != nil {
			c.Close(context.WithoutCancel(ctx))
			return nil, wrapErr(err)
		}
	}
	return &Listener{c: c}, nil
}

// Wait returns the next notification.
func (l *Listener) Wait(ctx context.Context) (*Notification, error) {
	n, err := l.c.WaitForNotification(ctx)
	if err != nil {
		return nil, wrapErr(err)
	}
	return &Notification{PID: n.PID, Channel: n.Channel, Payload: n.Payload}, nil
}

// Broken reports whether the listener must be reopened.
func (l *Listener) Broken() bool { return l.c.IsClosed() }

// Close closes the listener connection.
func (l *Listener) Close() error { return l.c.Close(context.Background()) }
