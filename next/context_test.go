package next_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stephenafamo/bob"
	n "github.com/stephenafamo/bob/next"
	"github.com/stephenafamo/bob/next/bobexec"
	"github.com/stephenafamo/bob/next/examples/models"
	"github.com/stephenafamo/bob/next/examples/models/relations/postsuser"
)

func TestFullRowContextQueries(t *testing.T) {
	db := database(t)
	ctx := n.WithExecutor(context.Background(), db, n.ExecutionConfig{Dialect: n.SQLite})
	u := models.UsersAs("u")
	base := u.Query().Where(u.Tenant.EQ(7)).OrderBy(u.ID.Asc())
	rows, err := base.Limit(1).AllContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	require(t, rows, []*models.UsersRow{{ID: 1, Tenant: 7, Name: "Alice", Version: 1, Fingerprint: 2}})
	rows, err = base.Offset(1).Limit(1).AllContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	require(t, rows[0].Name, "Bob")
	count, err := base.CountContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	require(t, count, int64(2))
	// Limit and offset above must not mutate the reusable base.
	rows, err = base.AllContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	require(t, len(rows), 2)
	row, err := base.Where(u.ID.EQ(1)).OneContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	require(t, row.Fingerprint, int64(2))
	if _, err := base.OneContext(ctx); err == nil {
		t.Fatal("OneContext silently selected one of multiple rows")
	}
	if _, err := base.Where(u.ID.EQ(99)).OneContext(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	stop := errors.New("stop streaming")
	if err := base.EachContext(ctx, func(*models.UsersRow) error { return stop }); !errors.Is(err, stop) {
		t.Fatal(err)
	}
	// A single-connection pool would block here if early exit leaked the cursor.
	if _, err := base.CountContext(ctx); err != nil {
		t.Fatal(err)
	}
	p := models.PostsAs("p")
	joined := base.JoinRelation(postsuser.Relation.Reverse(), u.Ref, p.Ref, false)
	count, err = joined.CountContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	require(t, count, int64(2))
	if _, err := joined.Limit(1).AllContext(ctx); err == nil {
		t.Fatal("pagination over multiplying joins accepted")
	}
	rows, err = u.Query().Where(u.Tenant.EQ(7)).SeekAfter(n.Cursor(u.Tenant, int64(7), false), n.Cursor(u.ID, int64(1), false)).AllContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	require(t, len(rows), 1)
	require(t, rows[0].Name, "Bob")
}

func TestFullRowContextMutations(t *testing.T) {
	db := database(t)
	ctx := n.WithExecutor(context.Background(), db, n.ExecutionConfig{Dialect: n.SQLite})
	u := models.UsersAs("u")
	input := models.UsersInsert{ID: 3, Tenant: 7, Name: "Inserted"}
	row, err := u.Insert(input).OneContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	require(t, row, &models.UsersRow{ID: 3, Tenant: 7, Name: "Inserted", Version: 1, Fingerprint: 6})
	patch := models.UsersUpdate{Name: n.Set("Updated")}
	row, err = u.Update(patch).Where(u.Tenant.EQ(7), u.ID.EQ(3)).OneContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	require(t, row.Name, "Updated")
	if _, err := u.Update(patch).ExecContext(ctx); err == nil {
		t.Fatal("unscoped update accepted")
	}
	if err := u.Update(patch).Where(u.Tenant.EQ(8), u.ID.EQ(3)).ExecOneContext(ctx); !errors.Is(err, n.ErrNotMatched) {
		t.Fatal(err)
	}
	if err := u.Delete().Where(u.Tenant.EQ(7), u.ID.EQ(3)).ExecOneContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Query().Where(u.Tenant.EQ(7), u.ID.EQ(3)).OneContext(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
}

func TestContextExecutorScopeAndTransactionOwnership(t *testing.T) {
	db := database(t)
	parent := n.WithExecutor(context.Background(), db, n.ExecutionConfig{Dialect: n.SQLite})
	u := models.UsersAs("u")
	query := u.Query().Where(u.Tenant.EQ(7), u.ID.EQ(1))
	var typedNil *sql.Tx
	for _, ctx := range []context.Context{context.Background(), n.WithExecutor(parent, nil, n.ExecutionConfig{}), n.WithExecutor(parent, typedNil, n.ExecutionConfig{})} {
		if _, err := query.AllContext(ctx); !errors.Is(err, n.ErrNoExecutor) {
			t.Fatal(err)
		}
		if _, err := query.OneContext(ctx); !errors.Is(err, n.ErrNoExecutor) {
			t.Fatal(err)
		}
		if _, err := query.CountContext(ctx); !errors.Is(err, n.ErrNoExecutor) {
			t.Fatal(err)
		}
		if err := query.EachContext(ctx, func(*models.UsersRow) error { return nil }); !errors.Is(err, n.ErrNoExecutor) {
			t.Fatal(err)
		}
		if _, err := n.ExecContext(ctx, n.SQL("invalid")); !errors.Is(err, n.ErrNoExecutor) {
			t.Fatal(err)
		}
		if err := n.ExecOneContext(ctx, n.SQL("invalid")); !errors.Is(err, n.ErrNoExecutor) {
			t.Fatal(err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var events []n.Event
	child := n.WithExecutor(parent, tx, n.ExecutionConfig{Dialect: n.SQLite, Observer: func(_ context.Context, event n.Event) { events = append(events, event) }})
	if err := n.ExecOneContext(child, n.Update(u.Ref, u.Name.Set("Uncommitted")).Where(u.Tenant.EQ(7), u.ID.EQ(1))); err != nil {
		t.Fatal(err)
	}
	row, err := query.OneContext(child)
	if err != nil {
		t.Fatal(err)
	}
	require(t, row.Name, "Uncommitted")
	for _, event := range events {
		if !event.InTransaction {
			t.Fatal("transaction scope lost in observation")
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := query.OneContext(child); !errors.Is(err, sql.ErrTxDone) {
		t.Fatal("expired transaction fell back to another executor", err)
	}
	row, err = query.OneContext(parent)
	if err != nil {
		t.Fatal(err)
	}
	require(t, row.Name, "Alice")
}

func TestBobExecutorBridgeKeepsSameTransaction(t *testing.T) {
	db := database(t)
	parent := bobexec.WithExecutor(context.Background(), bob.NewDB(db), n.ExecutionConfig{Dialect: n.SQLite})
	tx, err := bob.NewDB(db).Begin(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(parent)
	ctx := bobexec.WithExecutor(parent, tx, n.ExecutionConfig{Dialect: n.SQLite, InTransaction: true})
	exec, err := bob.ExecutorFromContext(ctx)
	if err != nil || exec != tx {
		t.Fatal("original Bob context executor changed", err)
	}
	u := models.UsersAs("u")
	if err := n.ExecOneContext(ctx, n.Update(u.Ref, u.Name.Set("Through bridge")).Where(u.Tenant.EQ(7), u.ID.EQ(1))); err != nil {
		t.Fatal(err)
	}
	row, err := u.Query().Where(u.Tenant.EQ(7), u.ID.EQ(1)).OneContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	require(t, row.Name, "Through bridge")
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	row, err = u.Query().Where(u.Tenant.EQ(7), u.ID.EQ(1)).OneContext(parent)
	if err != nil {
		t.Fatal(err)
	}
	require(t, row.Name, "Alice")
	var typedNil *bob.Tx
	for _, exec := range []bob.Executor{nil, typedNil} {
		ctx := bobexec.WithExecutor(parent, exec, n.ExecutionConfig{})
		if _, err := u.Query().AllContext(ctx); !errors.Is(err, n.ErrNoExecutor) {
			t.Fatal(err)
		}
		if _, err := bob.ExecutorFromContext(ctx); !errors.Is(err, bob.ErrNoExecutor) {
			t.Fatal(err)
		}
	}
}
