package bob

import (
	"context"
	"database/sql"
	"errors"

	"github.com/stephenafamo/scan"
)

// ErrNoExecutor is returned by context-only query operations when no executor
// has been attached with WithExecutor.
var ErrNoExecutor = errors.New("bob: no executor in context")

type executorContextKey struct{}

type executorContextValue struct{ exec Executor }

// WithExecutor returns a child context containing exec. It does not start or
// manage a transaction. The caller owns the executor's lifetime; a context holding
// a transaction must only be used while that transaction is open.
// A nil executor masks any executor in the parent context.
func WithExecutor(ctx context.Context, exec Executor) context.Context {
	return context.WithValue(ctx, executorContextKey{}, executorContextValue{exec: exec})
}

// ExecutorFromContext returns the executor attached by WithExecutor, or
// ErrNoExecutor if none is available. It never falls back to a global connection.
func ExecutorFromContext(ctx context.Context) (Executor, error) {
	value, ok := ctx.Value(executorContextKey{}).(executorContextValue)
	if !ok || value.exec == nil {
		return nil, ErrNoExecutor
	}
	return value.exec, nil
}

// ExecContext executes q using the executor attached to ctx with WithExecutor.
func ExecContext(ctx context.Context, q Query) (sql.Result, error) {
	exec, err := ExecutorFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return Exec(ctx, exec, q)
}

// OneContext scans one row using the executor attached to ctx with WithExecutor.
func OneContext[T any](ctx context.Context, q Query, m scan.Mapper[T]) (T, error) {
	exec, err := ExecutorFromContext(ctx)
	if err != nil {
		return *new(T), err
	}
	return One(ctx, exec, q, m)
}

// AllContext scans all rows using the executor attached to ctx with WithExecutor.
func AllContext[T any](ctx context.Context, q Query, m scan.Mapper[T]) ([]T, error) {
	return AllxContext[SliceTransformer[T, []T]](ctx, q, m)
}

// AllxContext is Allx using the executor attached to ctx with WithExecutor.
func AllxContext[Tr Transformer[T, V], T, V any](ctx context.Context, q Query, m scan.Mapper[T]) (V, error) {
	exec, err := ExecutorFromContext(ctx)
	if err != nil {
		return *new(V), err
	}
	return Allx[Tr](ctx, exec, q, m)
}

// CursorContext opens a cursor using the executor attached to ctx with WithExecutor.
// The cursor must be consumed or closed before the executor's lifetime ends.
func CursorContext[T any](ctx context.Context, q Query, m scan.Mapper[T]) (scan.ICursor[T], error) {
	exec, err := ExecutorFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return Cursor(ctx, exec, q, m)
}

// EachContext returns an iterator using the executor attached to ctx with WithExecutor.
// The iterator must be consumed before the executor's lifetime ends.
func EachContext[T any](ctx context.Context, q Query, m scan.Mapper[T]) (func(func(T, error) bool), error) {
	exec, err := ExecutorFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return Each(ctx, exec, q, m)
}
