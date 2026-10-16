package orm_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite"
	sqlitedialect "github.com/stephenafamo/bob/dialect/sqlite/dialect"
	"github.com/stephenafamo/bob/orm"
	"github.com/stephenafamo/scan"
)

type contextQueryRows []int

type contextExpr struct {
	rawExpr
	bob.Load
}

func contextQueryBase(sql string) bob.BaseQuery[*contextExpr] {
	return bob.BaseQuery[*contextExpr]{
		Expression: &contextExpr{rawExpr: rawExpr{sql: sql}},
		Dialect:    sqlitedialect.Dialect,
	}
}

func TestContextQueriesUseTransaction(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "CREATE TABLE items (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	tx, err := bob.NewDB(db).Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Errorf("cleanup rollback: %v", err)
		}
	}()
	txCtx := bob.WithExecutor(ctx, tx)

	// Insert through the context-only method; every subsequent query must see
	// this uncommitted row on the same transaction.
	result, err := contextQueryBase("INSERT INTO items VALUES (7)").ExecContext(txCtx)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("insert: %d, %v", n, err)
	}

	hooks := &bob.Hooks[*contextExpr, bob.SkipQueryHooksKey]{}
	hookCalls, loadCalls := 0, 0
	assertExecutor := func(ctx context.Context, exec bob.Executor) {
		t.Helper()
		fromCtx, err := bob.ExecutorFromContext(ctx)
		if err != nil || fromCtx != exec || exec != tx {
			t.Fatalf("hook/loader executor differs from transaction: %v", err)
		}
	}
	hooks.AppendHooks(func(ctx context.Context, exec bob.Executor, _ *contextExpr) (context.Context, error) {
		assertExecutor(ctx, exec)
		hookCalls++
		return ctx, nil
	})
	base := contextQueryBase("SELECT id FROM items ORDER BY id")
	base.Expression.AppendLoader(bob.LoaderFunc(func(ctx context.Context, exec bob.Executor, _ any) error {
		assertExecutor(ctx, exec)
		loadCalls++
		// Nested context-only calls must also use the same transaction.
		value, err := bob.OneContext(ctx, contextQueryBase("SELECT id FROM items"), scan.SingleColumnMapper[int])
		if err != nil || value != 7 {
			t.Fatalf("nested read: %d, %v", value, err)
		}
		return nil
	}))
	model := orm.ModelQuery[*contextExpr, int, contextQueryRows]{
		BaseQuery: base, Hooks: hooks, Scanner: scan.SingleColumnMapper[int],
	}
	general := orm.Query[*contextExpr, int, contextQueryRows, bob.SliceTransformer[int, contextQueryRows]]{
		ExecQuery: orm.ExecQuery[*contextExpr]{BaseQuery: base, Hooks: hooks},
		Scanner:   scan.SingleColumnMapper[int],
	}
	type rowQuery interface {
		OneContext(context.Context) (int, error)
		AllContext(context.Context) (contextQueryRows, error)
		CursorContext(context.Context) (scan.ICursor[int], error)
		EachContext(context.Context) (func(func(int, error) bool), error)
	}
	for name, query := range map[string]rowQuery{"model": model, "general": general} {
		t.Run(name, func(t *testing.T) {
			if v, err := query.OneContext(txCtx); err != nil || v != 7 {
				t.Fatalf("One: %d, %v", v, err)
			}
			if v, err := query.AllContext(txCtx); err != nil || !reflect.DeepEqual(v, contextQueryRows{7}) {
				t.Fatalf("All: %v, %v", v, err)
			}
			cursor, err := query.CursorContext(txCtx)
			if err != nil {
				t.Fatal(err)
			}
			defer cursor.Close()
			if !cursor.Next() {
				t.Fatalf("Cursor.Next: %v", cursor.Err())
			}
			if v, err := cursor.Get(); err != nil || v != 7 {
				t.Fatalf("Cursor.Get: %d, %v", v, err)
			}
			if err := cursor.Close(); err != nil {
				t.Fatal(err)
			}
			each, err := query.EachContext(txCtx)
			if err != nil {
				t.Fatal(err)
			}
			var values []int
			for v, err := range each {
				if err != nil {
					t.Fatal(err)
				}
				values = append(values, v)
			}
			if !reflect.DeepEqual(values, []int{7}) {
				t.Fatalf("Each: %v", values)
			}
			if _, err := query.OneContext(ctx); !errors.Is(err, bob.ErrNoExecutor) {
				t.Fatalf("missing executor: %v", err)
			}
		})
	}
	if hookCalls != 8 || loadCalls != 8 {
		t.Fatalf("hooks/loaders: %d/%d, want 8/8", hookCalls, loadCalls)
	}

	canceledCtx, cancel := context.WithCancel(txCtx)
	cancel()
	if _, err := model.OneContext(canceledCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query: %v", err)
	}

	// Exercise standalone helpers, including transformed/named slice results.
	if v, err := bob.AllContext(txCtx, base, scan.SingleColumnMapper[int]); err != nil || !reflect.DeepEqual(v, []int{7}) {
		t.Fatalf("AllContext: %v, %v", v, err)
	}
	if v, err := bob.AllxContext[bob.SliceTransformer[int, contextQueryRows]](txCtx, base, scan.SingleColumnMapper[int]); err != nil || !reflect.DeepEqual(v, contextQueryRows{7}) {
		t.Fatalf("AllxContext: %v, %v", v, err)
	}

	// View-specific Count/Exists also use the context executor.
	view := &sqlite.ViewQuery[int, contextQueryRows]{ModelQuery: orm.ModelQuery[*sqlitedialect.SelectQuery, int, contextQueryRows]{
		BaseQuery: sqlite.Select(), Scanner: scan.SingleColumnMapper[int],
	}}
	view.Expression.AppendSelect(sqlite.Quote("id"))
	view.Expression.SetTable(sqlite.Quote("items"))
	if n, err := view.CountContext(txCtx); err != nil || n != 1 {
		t.Fatalf("Count: %d, %v", n, err)
	}
	if exists, err := view.ExistsContext(txCtx); err != nil || !exists {
		t.Fatalf("Exists: %t, %v", exists, err)
	}
	if _, err := view.CountContext(ctx); !errors.Is(err, bob.ErrNoExecutor) {
		t.Fatalf("missing count executor: %v", err)
	}

	// Both ORM write paths preserve affected-row results.
	update := contextQueryBase("UPDATE items SET id = 8")
	execQuery := orm.ExecQuery[*contextExpr]{BaseQuery: update}
	if n, err := execQuery.ExecContext(txCtx); err != nil || n != 1 {
		t.Fatalf("ExecQuery: %d, %v", n, err)
	}
	modelWrite := orm.ModelQuery[*contextExpr, int, contextQueryRows]{BaseQuery: update}
	if n, err := modelWrite.ExecContext(txCtx); err != nil || n != 1 {
		t.Fatalf("ModelQuery.Exec: %d, %v", n, err)
	}

	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM items").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("write escaped transaction: count %d", count)
	}
	if _, err := model.OneContext(txCtx); !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("closed transaction: %v", err)
	}
}
