package orm_test

import (
	"context"
	"database/sql"
	"io"
	"testing"

	"github.com/stephenafamo/scan"

	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/psql"
	"github.com/stephenafamo/bob/dialect/psql/dialect"
	"github.com/stephenafamo/bob/dialect/psql/sm"
	sqlitedialect "github.com/stephenafamo/bob/dialect/sqlite/dialect"
	"github.com/stephenafamo/bob/orm"
	testutils "github.com/stephenafamo/bob/test/utils"
	_ "modernc.org/sqlite"
)

type rawExpr struct{ sql string }

func (r rawExpr) WriteSQL(_ context.Context, w io.StringWriter, _ bob.Dialect, _ int) ([]any, error) {
	w.WriteString(r.sql)
	return nil, nil
}

func (r *rawExpr) Clone() *rawExpr {
	clone := *r
	return &clone
}

// newModQuery builds a ModQuery whose generated Mod reproduces
// "SELECT id FROM todo", mirroring what the queries plugin emits. Extra funcs
// run inside the Mod after the SELECT/FROM, so a test can add the clauses a
// generated query already carries.
func newModQuery(scanner scan.Mapper[int], extra ...func(*dialect.SelectQuery)) orm.ModQuery[*dialect.SelectQuery, rawExpr, int, []int, bob.SliceTransformer[int, []int]] {
	return orm.ModQuery[*dialect.SelectQuery, rawExpr, int, []int, bob.SliceTransformer[int, []int]]{
		Query: orm.Query[rawExpr, int, []int, bob.SliceTransformer[int, []int]]{
			ExecQuery: orm.ExecQuery[rawExpr]{
				BaseQuery: bob.BaseQuery[rawExpr]{
					Expression: rawExpr{sql: "SELECT id FROM todo"},
					Dialect:    dialect.Dialect,
					QueryType:  bob.QueryTypeSelect,
				},
			},
			Scanner: scanner,
		},
		Mod: bob.ModFunc[*dialect.SelectQuery](func(q *dialect.SelectQuery) {
			q.AppendSelect(psql.Quote("id"))
			q.SetTable(psql.Quote("todo"))
			for _, fn := range extra {
				fn(q)
			}
		}),
		Build: psql.Select,
	}
}

func TestModQueryWith(t *testing.T) {
	mq := newModQuery(nil)

	examples := testutils.Testcases{
		"base query from generated mod": {
			Doc:         "With() and no extra mods reproduces the base query from the generated Mod alone",
			ExpectedSQL: `SELECT id FROM todo`,
			Query:       mq.With(),
		},
		"augmented with extra mods": {
			Doc:          "Extra mods are appended on top of the generated Mod",
			ExpectedSQL:  `SELECT id FROM todo WHERE (project_id = $1) LIMIT 10`,
			ExpectedArgs: []any{1},
			Query: mq.With(
				sm.Where(psql.Quote("project_id").EQ(psql.Arg(1))),
				sm.Limit(10),
			),
		},
	}

	testutils.RunTests(t, examples, nil)
}

// TestModQueryWithExistingClauses augments a generated query that already
// carries clauses: WHERE conditions are ANDed, ORDER BY merges into one clause,
// and LIMIT / OFFSET are replaced.
func TestModQueryWithExistingClauses(t *testing.T) {
	examples := testutils.Testcases{
		"existing WHERE is ANDed with one extra condition": {
			Doc:          "A user sm.Where() ANDs onto the existing WHERE",
			ExpectedSQL:  `SELECT id FROM todo WHERE done = $1 AND project_id = $2`,
			ExpectedArgs: []any{true, 1},
			Query: newModQuery(nil, func(q *dialect.SelectQuery) {
				q.AppendWhere(psql.Quote("done").EQ(psql.Arg(true)))
			}).With(
				sm.Where(psql.Quote("project_id").EQ(psql.Arg(1))),
			),
		},
		"existing WHERE is ANDed with multiple extra conditions": {
			Doc:          "Multiple user sm.Where() mods each AND on, preserving arg order",
			ExpectedSQL:  `SELECT id FROM todo WHERE done = $1 AND project_id = $2 AND priority > $3`,
			ExpectedArgs: []any{true, 1, 2},
			Query: newModQuery(nil, func(q *dialect.SelectQuery) {
				q.AppendWhere(psql.Quote("done").EQ(psql.Arg(true)))
			}).With(
				sm.Where(psql.Quote("project_id").EQ(psql.Arg(1))),
				sm.Where(psql.Quote("priority").GT(psql.Arg(2))),
			),
		},
		"existing ORDER BY merges with the user ORDER BY": {
			Doc:         "A user sm.OrderBy() appends into the existing ORDER BY clause",
			ExpectedSQL: `SELECT id FROM todo ORDER BY created_at, id`,
			Query: newModQuery(nil, func(q *dialect.SelectQuery) {
				q.AppendOrder(psql.Quote("created_at"))
			}).With(
				sm.OrderBy(psql.Quote("id")),
			),
		},
		"existing LIMIT is replaced by the user LIMIT": {
			Doc:         "A user sm.Limit() replaces the existing LIMIT",
			ExpectedSQL: `SELECT id FROM todo LIMIT 10`,
			Query: newModQuery(nil, func(q *dialect.SelectQuery) {
				q.SetLimit(5)
			}).With(
				sm.Limit(10),
			),
		},
		"existing OFFSET is replaced by the user OFFSET": {
			Doc:         "A user sm.Offset() replaces the existing OFFSET",
			ExpectedSQL: `SELECT id FROM todo OFFSET 10`,
			Query: newModQuery(nil, func(q *dialect.SelectQuery) {
				q.SetOffset(5)
			}).With(
				sm.Offset(10),
			),
		},
		"WHERE ANDs, ORDER BY merges, LIMIT is replaced": {
			Doc:          "Combined scenario across all three behaviors",
			ExpectedSQL:  `SELECT id FROM todo WHERE done = $1 AND project_id = $2 ORDER BY created_at, id LIMIT 10`,
			ExpectedArgs: []any{true, 1},
			Query: newModQuery(nil, func(q *dialect.SelectQuery) {
				q.AppendWhere(psql.Quote("done").EQ(psql.Arg(true)))
				q.AppendOrder(psql.Quote("created_at"))
				q.SetLimit(5)
			}).With(
				sm.Where(psql.Quote("project_id").EQ(psql.Arg(1))),
				sm.OrderBy(psql.Quote("id")),
				sm.Limit(10),
			),
		},
	}

	testutils.RunTests(t, examples, nil)
}

func TestModQueryWithPreservesScanner(t *testing.T) {
	scannerCalled := false
	scanner := func(context.Context, []string) (func(*scan.Row) (any, error), func(any) (int, error)) {
		scannerCalled = true
		return nil, nil
	}

	augmented := newModQuery(scanner).With()
	if augmented.Scanner == nil {
		t.Fatal("With() dropped the scanner")
	}

	augmented.Scanner(context.Background(), nil)
	if !scannerCalled {
		t.Fatal("With() did not preserve the original scanner")
	}
}

func TestQueryClonePreservesScanner(t *testing.T) {
	scannerCalled := false
	scanner := func(context.Context, []string) (func(*scan.Row) (any, error), func(any) (int, error)) {
		scannerCalled = true
		return nil, nil
	}

	cloned := newModQuery(scanner).Query.Clone()
	if cloned.Scanner == nil {
		t.Fatal("Clone() dropped the scanner")
	}

	cloned.Scanner(context.Background(), nil)
	if !scannerCalled {
		t.Fatal("Clone() did not preserve the original scanner")
	}
}

func TestModelQueryClonePreservesScannerAndHooks(t *testing.T) {
	scannerCalled := false
	scanner := func(context.Context, []string) (func(*scan.Row) (any, error), func(any) (int, error)) {
		scannerCalled = true
		return nil, nil
	}
	hooks := bob.Hooks[rawExpr, bob.SkipQueryHooksKey]{}
	query := orm.ModelQuery[rawExpr, int, []int]{
		BaseQuery: bob.BaseQuery[rawExpr]{
			Expression: rawExpr{sql: "SELECT id FROM todo"},
		},
		Hooks:   &hooks,
		Scanner: scanner,
	}

	cloned := query.Clone()
	if cloned.Hooks != &hooks {
		t.Fatal("Clone() dropped the query hooks")
	}
	if cloned.Scanner == nil {
		t.Fatal("Clone() dropped the scanner")
	}
	cloned.Scanner(context.Background(), nil)
	if !scannerCalled {
		t.Fatal("Clone() did not preserve the original scanner")
	}
}

func TestModelQueryWithDoesNotMutateOriginal(t *testing.T) {
	query := orm.ModelQuery[*rawExpr, int, []int]{
		BaseQuery: bob.BaseQuery[*rawExpr]{
			Expression: &rawExpr{sql: "SELECT id FROM todo"},
		},
	}

	updated := query.With(bob.ModFunc[*rawExpr](func(expr *rawExpr) {
		expr.sql = "SELECT id FROM done"
	}))

	if query.Expression.sql != "SELECT id FROM todo" {
		t.Fatalf("With() mutated original query: %q", query.Expression.sql)
	}
	if updated.Expression.sql != "SELECT id FROM done" {
		t.Fatalf("With() did not apply the mod: %q", updated.Expression.sql)
	}
}

type modelQueryRow struct {
	ID int `db:"id"`
}

type modelQueryRows []*modelQueryRow

var modelQueryRowsHookCalls int

func (modelQueryRows) AfterQueryHook(context.Context, bob.Executor, bob.QueryType) error {
	modelQueryRowsHookCalls++
	return nil
}

func TestModelQueryTypedOperations(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY); INSERT INTO items (id) VALUES (1), (2)`); err != nil {
		t.Fatal(err)
	}
	exec := bob.NewDB(db)

	hooks := bob.Hooks[*rawExpr, bob.SkipQueryHooksKey]{}
	hookCalls := 0
	hooks.AppendHooks(func(ctx context.Context, _ bob.Executor, _ *rawExpr) (context.Context, error) {
		hookCalls++
		return ctx, nil
	})

	newQuery := func(query string, queryType bob.QueryType) orm.ModelQuery[*rawExpr, *modelQueryRow, modelQueryRows] {
		return orm.ModelQuery[*rawExpr, *modelQueryRow, modelQueryRows]{
			BaseQuery: bob.BaseQuery[*rawExpr]{
				Expression: &rawExpr{sql: query},
				Dialect:    sqlitedialect.Dialect,
				QueryType:  queryType,
			},
			Hooks:   &hooks,
			Scanner: scan.StructMapper[*modelQueryRow](),
		}
	}

	ctx := context.Background()

	one, err := newQuery("SELECT id FROM items ORDER BY id LIMIT 1", bob.QueryTypeSelect).One(ctx, exec)
	if err != nil {
		t.Fatal(err)
	}
	if one.ID != 1 {
		t.Fatalf("One() returned id %d", one.ID)
	}

	modelQueryRowsHookCalls = 0
	all, err := newQuery("SELECT id FROM items ORDER BY id", bob.QueryTypeSelect).All(ctx, exec)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != 1 || all[1].ID != 2 {
		t.Fatalf("All() returned %#v", all)
	}
	if modelQueryRowsHookCalls != 1 {
		t.Fatalf("All() ran named-slice hooks %d times", modelQueryRowsHookCalls)
	}

	cursor, err := newQuery("SELECT id FROM items ORDER BY id", bob.QueryTypeSelect).Cursor(ctx, exec)
	if err != nil {
		t.Fatal(err)
	}
	if !cursor.Next() {
		t.Fatalf("Cursor() had no first row: %v", cursor.Err())
	}
	first, err := cursor.Get()
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != 1 {
		t.Fatalf("Cursor() returned id %d", first.ID)
	}
	if err := cursor.Close(); err != nil {
		t.Fatal(err)
	}

	each, err := newQuery("SELECT id FROM items ORDER BY id", bob.QueryTypeSelect).Each(ctx, exec)
	if err != nil {
		t.Fatal(err)
	}
	var eachIDs []int
	each(func(row *modelQueryRow, err error) bool {
		if err != nil {
			t.Errorf("Each() returned error: %v", err)
			return false
		}
		eachIDs = append(eachIDs, row.ID)
		return true
	})
	if len(eachIDs) != 2 || eachIDs[0] != 1 || eachIDs[1] != 2 {
		t.Fatalf("Each() returned ids %v", eachIDs)
	}

	rowsAffected, err := newQuery("UPDATE items SET id = id + 10", bob.QueryTypeUpdate).Exec(ctx, exec)
	if err != nil {
		t.Fatal(err)
	}
	if rowsAffected != 2 {
		t.Fatalf("Exec() affected %d rows", rowsAffected)
	}
	if hookCalls != 5 {
		t.Fatalf("query hooks ran %d times, want 5", hookCalls)
	}
}
