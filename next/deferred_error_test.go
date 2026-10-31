package next_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	n "github.com/stephenafamo/bob/next"
)

type deferredErrorRows struct {
	err    error
	closed int
}

func (r *deferredErrorRows) Scan(...any) error          { return errors.New("no row") }
func (r *deferredErrorRows) Columns() ([]string, error) { return nil, nil }
func (r *deferredErrorRows) Next() bool                 { return false }
func (r *deferredErrorRows) Close() error               { r.closed++; return nil }
func (r *deferredErrorRows) Err() error {
	if r.closed > 0 {
		return r.err
	}
	return nil
}

type deferredErrorExecutor struct{ rows *deferredErrorRows }

func (e deferredErrorExecutor) QueryRowsContext(context.Context, string, ...any) (n.Rows, error) {
	return e.rows, nil
}
func (e deferredErrorExecutor) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errors.New("unexpected exec")
}

var _ n.Rows = (*deferredErrorRows)(nil)
var _ n.RowsExecutor = deferredErrorExecutor{}

func TestReturningErrorAfterCursorClosePreservesSQLState(t *testing.T) {
	original := &pgconn.PgError{Code: "23503", ConstraintName: "expected_fk"}
	rows := &deferredErrorRows{err: original}
	ctx := n.WithRowsExecutor(context.Background(), deferredErrorExecutor{rows}, n.ExecutionConfig{})
	_, err := n.OneContext(ctx, n.SQL("INSERT INTO example VALUES(1) RETURNING id"), n.CountProjection())
	require(t, errors.Is(err, original), true)
	databaseError, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		t.Fatal("database error lost", err)
	}
	require(t, databaseError.ConstraintName, "expected_fk")
	require(t, rows.closed, 1)
}
