package next

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
)

var ErrNoExecutor = errors.New("bob next: no executor in context")

// ExecutionConfig belongs at the database integration boundary. InTransaction
// describes an externally owned transaction for observation; it starts none.
// Its zero value selects PostgreSQL and disables observation.
type ExecutionConfig struct {
	Dialect       Dialect
	Observer      Observer
	InTransaction bool
}

type (
	executionKey   struct{}
	executionValue struct{ session Session }
)

// WithExecutor binds a database/sql executor without taking ownership of it.
// The resulting context must not outlive an attached transaction. A nil executor
// masks a parent binding rather than falling back to another connection.
func WithExecutor(ctx context.Context, exec Executor, config ExecutionConfig) context.Context {
	if nilExecutor(exec) {
		return WithRowsExecutor(ctx, nil, config)
	}
	_, tx := exec.(*sql.Tx)
	config.InTransaction = config.InTransaction || tx
	return WithRowsExecutor(ctx, sqlExecutor{exec}, config)
}

// WithRowsExecutor is the binding point for native driver integrations.
func WithRowsExecutor(ctx context.Context, exec RowsExecutor, config ExecutionConfig) context.Context {
	if nilExecutor(exec) {
		exec = nil
	}
	return context.WithValue(ctx, executionKey{}, executionValue{Session{exec, config.Dialect, config.Observer, config.InTransaction}})
}

func nilExecutor(exec any) bool {
	if exec == nil {
		return true
	}
	v := reflect.ValueOf(exec)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

func sessionFromContext(ctx context.Context) (Session, error) {
	v, ok := ctx.Value(executionKey{}).(executionValue)
	if !ok || v.session.executor == nil {
		return Session{}, ErrNoExecutor
	}
	return v.session, nil
}

func ExecContext(ctx context.Context, q Buildable) (int64, error) {
	s, err := sessionFromContext(ctx)
	if err != nil {
		return 0, err
	}
	return s.Exec(ctx, q)
}

func ExecOneContext(ctx context.Context, q Buildable) error {
	s, err := sessionFromContext(ctx)
	if err != nil {
		return err
	}
	return s.ExecOne(ctx, q)
}

// AllContext executes an explicitly shaped query using the bound executor.
// Full-row table reads normally use the generated c.Query().AllContext instead.
func AllContext[T any](ctx context.Context, q Buildable, p Projection[T]) ([]T, error) {
	s, err := sessionFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return All(ctx, s, q, p)
}

func OneContext[T any](ctx context.Context, q Buildable, p Projection[T]) (T, error) {
	s, err := sessionFromContext(ctx)
	if err != nil {
		return *new(T), err
	}
	return One(ctx, s, q, p)
}
