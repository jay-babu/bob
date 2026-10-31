package next

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"
)

// Executor is implemented by both *sql.DB and *sql.Tx. No pool lookup or
// transaction creation occurs implicitly in query or relationship execution.
type Executor interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

var _ Executor = (*sql.DB)(nil)
var _ Executor = (*sql.Tx)(nil)

type Scanner interface{ Scan(...any) error }

// Rows is the cursor contract shared by database/sql and native driver adapters.
type Rows interface {
	Scanner
	Columns() ([]string, error)
	Next() bool
	Close() error
	Err() error
}

// RowsExecutor lets integrations retain their existing driver and transaction.
type RowsExecutor interface {
	QueryRowsContext(context.Context, string, ...any) (Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type sqlExecutor struct{ Executor }

func (e sqlExecutor) QueryRowsContext(ctx context.Context, query string, args ...any) (Rows, error) {
	return e.QueryContext(ctx, query, args...)
}

// Projection requires an explicit result shape. Generated full-row projections
// select every field; custom projections use their own destination struct.
type Projection[T any] struct {
	Columns []Selection
	Scan    func(Scanner) (T, error)
}

// Event excludes argument values. Query duration includes scanning; row count
// is the number scanned or affected. Observer callbacks must be concurrency safe.
type Event struct {
	SQL            string
	ParameterTypes []string
	Duration       time.Duration
	Rows           int64
	InTransaction  bool
	Err            error
}
type Observer func(context.Context, Event)

type Session struct {
	executor RowsExecutor
	dialect  Dialect
	observer Observer
	inTx     bool
}

func NewSession(exec Executor, dialect Dialect, observer Observer) Session {
	_, tx := exec.(*sql.Tx)
	return Session{sqlExecutor{exec}, dialect, observer, tx}
}

func (s Session) observe(ctx context.Context, stmt Statement, start time.Time, rows int64, err error) {
	if s.observer == nil {
		return
	}
	types := make([]string, len(stmt.Args))
	for i, a := range stmt.Args {
		if a == nil {
			types[i] = "<nil>"
		} else {
			types[i] = reflect.TypeOf(a).String()
		}
	}
	var safeErr error
	if err != nil {
		safeErr = errors.New("database operation failed")
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			safeErr = fmt.Errorf("database operation failed (SQLSTATE %s)", state.SQLState())
		}
	}
	s.observer(ctx, Event{stmt.SQL, types, time.Since(start), rows, s.inTx, safeErr})
}

func (s Session) finished(ctx context.Context, stmt Statement, start time.Time, count *int64, err *error) {
	if reason := recover(); reason != nil {
		s.observe(ctx, stmt, start, *count, errors.New("operation panicked"))
		panic(reason)
	}
	s.observe(ctx, stmt, start, *count, *err)
}

func (s Session) Exec(ctx context.Context, q Buildable) (affected int64, err error) {
	stmt, err := q.Build(s.dialect)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	defer s.finished(ctx, stmt, start, &affected, &err)
	args, err := driverArgs(stmt.Args)
	if err != nil {
		return 0, err
	}
	result, err := s.executor.ExecContext(ctx, stmt.SQL, args...)
	if err != nil {
		return 0, Classify(err)
	}
	return result.RowsAffected()
}

var ErrNotMatched = errors.New("no row matched the write predicate")

// ExecOne cannot distinguish missing rows from failed version predicates without
// another read. It does not invent that distinction or retry the write.
func (s Session) ExecOne(ctx context.Context, q Buildable) error {
	n, err := s.Exec(ctx, q)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotMatched
	}
	if n != 1 {
		return fmt.Errorf("expected one affected row, got %d (write has executed)", n)
	}
	return nil
}

func All[T any](ctx context.Context, s Session, q Buildable, p Projection[T]) ([]T, error) {
	var result []T
	err := Each(ctx, s, q, p, func(v T) error { result = append(result, v); return nil })
	if err != nil {
		return nil, err
	}
	return result, nil
}

func One[T any](ctx context.Context, s Session, q Buildable, p Projection[T]) (T, error) {
	var value T
	count := 0
	err := Each(ctx, s, q, p, func(v T) error {
		count++
		if count > 1 {
			return fmt.Errorf("expected one result, got multiple rows")
		}
		value = v
		return nil
	})
	if err == nil && count == 0 {
		err = sql.ErrNoRows
	}
	return value, err
}

// Each streams rows and closes the cursor on success, cancellation, scan failure,
// or early consumer exit. No reflection is used for mapping result values.
func Each[T any](ctx context.Context, s Session, q Buildable, p Projection[T], yield func(T) error) (err error) {
	if p.Scan == nil || yield == nil {
		return fmt.Errorf("projection scanner and consumer are required")
	}
	return s.each(ctx, q, p.Columns, func(row Scanner) (bool, error) {
		value, err := p.Scan(row)
		if err != nil {
			return false, err
		}
		return true, yield(value)
	})
}

// Keep execution and cursor management outside generic code: a schema with
// hundreds of row layouts must not compile a second executor for every layout.
func (s Session) each(ctx context.Context, q Buildable, columns []Selection, consume func(Scanner) (bool, error)) (err error) {
	stmt, err := q.Build(s.dialect)
	if err != nil {
		return err
	}
	start := time.Now()
	var count int64
	defer s.finished(ctx, stmt, start, &count, &err)
	args, err := driverArgs(stmt.Args)
	if err != nil {
		return err
	}
	rows, err := s.executor.QueryRowsContext(ctx, stmt.SQL, args...)
	if err != nil {
		return Classify(err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	actual, err := rows.Columns()
	if err != nil {
		return err
	}
	expected := make([]string, len(columns))
	seen := map[string]bool{}
	for i, c := range columns {
		expected[i] = identifierAlias(c.label, s.dialect)
		if expected[i] == "" {
			if n, ok := c.node.(columnNode); ok {
				expected[i] = n.name
			}
		}
		if expected[i] == "" || seen[expected[i]] {
			return fmt.Errorf("projection requires unique result labels")
		}
		seen[expected[i]] = true
	}
	if !slices.Equal(actual, expected) {
		return fmt.Errorf("result columns %v do not match projection %v", actual, expected)
	}
	for rows.Next() {
		decoded, err := consume(rows)
		if decoded {
			count++
		}
		if err != nil {
			return err
		}
	}
	return Classify(rows.Err())
}

// Resolve Valuer values before handing them to driver-specific encoders. In
// particular, pgx's fallback from a Valuer through a flat slice drops PostgreSQL
// array lower bounds; a standard string parameter uses its lossless text path.
func driverArgs(values []any) ([]any, error) {
	args := make([]any, len(values))
	for i, value := range values {
		if named, ok := value.(sql.NamedArg); ok {
			converted, err := driver.DefaultParameterConverter.ConvertValue(named.Value)
			if err != nil {
				return nil, fmt.Errorf("parameter %d: %w", i+1, err)
			}
			named.Value = converted
			args[i] = named
			continue
		}
		converted, err := driver.DefaultParameterConverter.ConvertValue(value)
		if err != nil {
			return nil, fmt.Errorf("parameter %d: %w", i+1, err)
		}
		args[i] = converted
	}
	return args, nil
}

// SQL allows SQL-defined queries to share scanning, transactions and observation.
// Its SQL is trusted application source, never user input. Placeholder syntax
// belongs to the chosen driver; no placeholder rewriting is attempted.
func SQL(text string, args ...any) Buildable { return sqlStatement{text, slices.Clone(args)} }

type sqlStatement struct {
	sql  string
	args []any
}

func (s sqlStatement) Build(Dialect) (Statement, error) {
	return Statement{SQL: s.sql, Args: slices.Clone(s.args)}, nil
}

// WithinTx gives the callback a session bound to exactly this transaction.
// A callback error or panic rolls it back; there are no automatic retries.
func WithinTx(ctx context.Context, db *sql.DB, d Dialect, opts *sql.TxOptions, observe Observer, fn func(Session) error) (err error) {
	tx, err := db.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = fn(NewSession(tx, d, observe)); err != nil {
		return err
	}
	return tx.Commit()
}

// DatabaseError exposes stable PostgreSQL SQLSTATE classification and preserves
// the original driver error for errors.As. Other drivers remain unclassified.
type DatabaseError struct {
	Code  string
	Cause error
}

func (e *DatabaseError) Error() string { return "database error (SQLSTATE " + e.Code + ")" }
func (e *DatabaseError) Unwrap() error { return e.Cause }
func Classify(err error) error {
	if err == nil {
		return nil
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		return &DatabaseError{state.SQLState(), err}
	}
	return err
}
