package next_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stephenafamo/bob"
	bobpgx "github.com/stephenafamo/bob/drivers/pgx"
	n "github.com/stephenafamo/bob/next"
	"github.com/stephenafamo/bob/next/bobexec"
	"github.com/stephenafamo/bob/next/examples/models"
	"github.com/stephenafamo/bob/next/internal/pgtest"
)

func TestNativePGXContextUsesDBExecTransaction(t *testing.T) {
	db := pgtest.Open(t)
	ctx := context.Background()
	if _, err := db.Exec(`CREATE TABLE users(id bigint NOT NULL,tenant bigint NOT NULL,name text NOT NULL,note text,version bigint NOT NULL DEFAULT 1,fingerprint bigint GENERATED ALWAYS AS(id*2) STORED,PRIMARY KEY(tenant,id))`); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, pgtest.DSN(t, db))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var events []n.Event
	bound := bobexec.WithExecutor(ctx, bobpgx.NewTx(tx, nil), n.ExecutionConfig{InTransaction: true, Observer: func(_ context.Context, event n.Event) { events = append(events, event) }})
	u := models.UsersAs("u")
	if _, err := u.Query().Where(n.ILike(u.Name.Expr(), "%name%")).Build(n.SQLite); err == nil {
		t.Fatal("ILIKE accepted for a dialect without the same matching semantics")
	}
	input := models.UsersInsert{ID: 1, Tenant: 7, Name: "Private name"}
	if err := n.ExecOneContext(bound, n.Insert(u.Ref, input.Assignments(u)...)); err != nil {
		t.Fatal(err)
	}
	row, err := u.Query().Where(u.Tenant.EQ(7), u.ID.EQ(1)).OneContext(bound)
	if err != nil {
		t.Fatal(err)
	}
	require(t, row.Name, input.Name)
	require(t, row.Fingerprint, int64(2))
	row, err = u.Query().Where(n.ILike(u.Name.Expr(), "%PRIVATE _ame")).OneContext(bound)
	if err != nil {
		t.Fatal(err)
	}
	require(t, row.Name, input.Name)
	// The original Bob executor sees exactly the same uncommitted transaction.
	exec, err := bob.ExecutorFromContext(bound)
	if err != nil {
		t.Fatal(err)
	}
	result, err := exec.ExecContext(bound, `UPDATE users SET note=$1 WHERE tenant=$2 AND id=$3`, "same transaction", int64(7), int64(1))
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatal("Bob and next used different transactions", err)
	}
	row, err = u.Query().Where(u.Tenant.EQ(7), u.ID.EQ(1)).OneContext(bound)
	if err != nil {
		t.Fatal(err)
	}
	require(t, row.Note.V, "same transaction")
	for _, event := range events {
		if !event.InTransaction {
			t.Fatal("native pgx transaction scope lost")
		}
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Query().AllContext(bound); !errors.Is(err, pgx.ErrTxClosed) {
		t.Fatal("closed native transaction fell back to a pool", err)
	}
	outside := n.WithExecutor(ctx, db, n.ExecutionConfig{})
	count, err := u.Query().CountContext(outside)
	if err != nil {
		t.Fatal(err)
	}
	require(t, count, int64(0))
}
