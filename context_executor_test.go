package bob

import (
	"context"
	"errors"
	"testing"

	"github.com/stephenafamo/scan"
)

type contextTestExecutor struct {
	NoopExecutor
	id int
}

func TestExecutorContextIsolation(t *testing.T) {
	parent := context.Background()
	a, b := &contextTestExecutor{id: 1}, &contextTestExecutor{id: 2}
	child := WithExecutor(parent, a)
	nested := WithExecutor(child, b)
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want Executor
	}{
		{"parent", parent, nil},
		{"child", child, a},
		{"nested override", nested, b},
		{"nil masks parent", WithExecutor(child, nil), nil},
		{"parent unchanged", child, a},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExecutorFromContext(tc.ctx)
			if tc.want == nil {
				if got != nil || !errors.Is(err, ErrNoExecutor) {
					t.Fatalf("got (%v, %v), want ErrNoExecutor", got, err)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("got (%v, %v), want %v", got, err, tc.want)
			}
		})
	}
}

func TestContextOperationsRequireExecutor(t *testing.T) {
	ctx := context.Background()
	// A nil query verifies lookup fails before hooks, building, or scanning.
	checks := map[string]func() error{
		"Exec": func() error { _, err := ExecContext(ctx, nil); return err },
		"One":  func() error { _, err := OneContext(ctx, nil, scan.SingleColumnMapper[int]); return err },
		"All":  func() error { _, err := AllContext(ctx, nil, scan.SingleColumnMapper[int]); return err },
		"Allx": func() error {
			_, err := AllxContext[SliceTransformer[int, []int]](ctx, nil, scan.SingleColumnMapper[int])
			return err
		},
		"Cursor": func() error { _, err := CursorContext(ctx, nil, scan.SingleColumnMapper[int]); return err },
		"Each":   func() error { _, err := EachContext(ctx, nil, scan.SingleColumnMapper[int]); return err },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check(); !errors.Is(err, ErrNoExecutor) {
				t.Fatalf("got %v, want ErrNoExecutor", err)
			}
		})
	}
}
