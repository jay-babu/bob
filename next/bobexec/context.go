// Package bobexec binds an existing Bob executor for both the original Bob API
// and next's context-only API. Use it at the DBExec transaction binding point;
// model packages and next's core do not depend on this adapter.
package bobexec

import (
	"context"
	"reflect"

	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/next"
)

type executor struct{ bob.Executor }

func (e executor) QueryRowsContext(ctx context.Context, query string, args ...any) (next.Rows, error) {
	return e.QueryContext(ctx, query, args...)
}

// WithExecutor preserves the original context executor for existing callers and
// attaches that same executor to next. It neither opens nor commits transactions.
func WithExecutor(ctx context.Context, exec bob.Executor, config next.ExecutionConfig) context.Context {
	if exec != nil {
		v := reflect.ValueOf(exec)
		switch v.Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
			if v.IsNil() {
				exec = nil
			}
		}
	}
	ctx = bob.WithExecutor(ctx, exec)
	if exec == nil {
		return next.WithRowsExecutor(ctx, nil, config)
	}
	return next.WithRowsExecutor(ctx, executor{exec}, config)
}
