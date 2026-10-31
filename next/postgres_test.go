package next_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
	n "github.com/stephenafamo/bob/next"
	"github.com/stephenafamo/bob/next/internal/pgtest"
)

func TestPostgreSQLExecution(t *testing.T) {
	db := pgtest.Open(t)
	ctx := context.Background()
	_, err := db.Exec(`CREATE TABLE records(tenant bigint NOT NULL,id bigint NOT NULL,amount numeric(40,10) NOT NULL,payload jsonb NOT NULL,labels text[] NOT NULL,moments timestamptz[] NOT NULL,binary_values bytea[] NOT NULL,note text,version bigint NOT NULL DEFAULT 1,fingerprint bigint GENERATED ALWAYS AS (id*2) STORED,PRIMARY KEY(tenant,id))`)
	if err != nil {
		t.Fatal(err)
	}
	table := n.DefineTable(n.TableInfo{Name: "records", PrimaryKey: []string{"tenant", "id"}, Columns: []n.ColumnInfo{{Name: "tenant"}, {Name: "id"}, {Name: "amount"}, {Name: "payload"}, {Name: "labels"}, {Name: "moments"}, {Name: "binary_values"}, {Name: "note", Nullable: true}, {Name: "version", HasDefault: true}, {Name: "fingerprint", Generated: true}}})
	r := table.As("r")
	tenant := n.Field[int64](r, "tenant")
	id := n.Field[int64](r, "id")
	amount := n.Field[n.Decimal](r, "amount")
	payload := n.Field[n.JSON[struct{ Name string }]](r, "payload")
	labels := n.Field[n.Array[string]](r, "labels")
	moments := n.Field[n.Array[time.Time]](r, "moments")
	binary := n.Field[n.Array[[]byte]](r, "binary_values")
	note := n.Field[string](r, "note")
	version := n.Field[int64](r, "version")
	fingerprint := n.Field[int64](r, "fingerprint")
	type row struct {
		Tenant, ID           int64
		Amount               n.Decimal
		Payload              n.JSON[struct{ Name string }]
		Labels               n.Array[string]
		Moments              n.Array[time.Time]
		Binary               n.Array[[]byte]
		Note                 sql.Null[string]
		Version, Fingerprint int64
	}
	p := n.Projection[row]{Columns: []n.Selection{tenant.Select("tenant"), id.Select("id"), amount.Select("amount"), payload.Select("payload"), labels.Select("labels"), moments.Select("moments"), binary.Select("binary_values"), note.Select("note"), version.Select("version"), fingerprint.Select("fingerprint")}, Scan: func(s n.Scanner) (row, error) {
		var value row
		err := s.Scan(&value.Tenant, &value.ID, &value.Amount, &value.Payload, &value.Labels, &value.Moments, &value.Binary, &value.Note, &value.Version, &value.Fingerprint)
		return value, err
	}}
	decimal, err := n.ParseDecimal("123456789012345678901234567890.1234567890")
	if err != nil {
		t.Fatal(err)
	}
	want := row{Tenant: 7, ID: 1, Amount: decimal, Payload: n.JSON[struct{ Name string }]{V: struct{ Name string }{"private"}}, Labels: n.Array[string]{Elements: []sql.Null[string]{{V: `comma,quote"slash\`, Valid: true}, {}, {V: "NULL", Valid: true}}, Dimensions: []n.Dimension{{Length: 3, LowerBound: 0}}}, Moments: n.Array[time.Time]{Elements: []sql.Null[time.Time]{{V: time.Date(2026, 10, 31, 12, 34, 56, 0, time.UTC), Valid: true}}, Dimensions: []n.Dimension{{Length: 1, LowerBound: 1}}}, Binary: n.Array[[]byte]{Elements: []sql.Null[[]byte]{{V: []byte{0, 255}, Valid: true}, {}}, Dimensions: []n.Dimension{{Length: 2, LowerBound: 1}}}, Version: 1, Fingerprint: 2}
	var events []n.Event
	s := n.NewSession(db, n.Postgres, func(_ context.Context, event n.Event) { events = append(events, event) })
	insert := n.Insert(r, tenant.Set(want.Tenant), id.Set(want.ID), amount.Set(want.Amount), payload.Set(want.Payload), labels.Set(want.Labels), moments.Set(want.Moments), binary.Set(want.Binary), note.AssignNullable(n.Null[string]())).Returning(p.Columns...)
	got, err := n.One(ctx, s, insert, p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip: got %#v; want %#v", got, want)
	}
	upsert := insert.OnConflict(tenant.Select(""), id.Select("")).DoUpdate(version.SetExpr(n.Add(version.Expr(), n.Value(int64(1))))).ConflictWhere(version.EQ(1))
	got, err = n.One(ctx, s, upsert, p)
	if err != nil {
		t.Fatal(err)
	}
	require(t, got.Version, int64(2))
	_, err = n.One(ctx, s, upsert, p)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if err = s.ExecOne(ctx, n.Update(r, note.Set("changed")).Where(tenant.EQ(7), id.EQ(1), version.EQ(1))); !errors.Is(err, n.ErrNotMatched) {
		t.Fatal(err)
	}
	_, err = n.One(ctx, s, insert, p)
	var classified *n.DatabaseError
	var original *pgconn.PgError
	if !errors.As(err, &classified) || classified.Code != "23505" || !errors.As(err, &original) {
		t.Fatalf("constraint error classification: %v", err)
	}
	for _, event := range events {
		if strings.Contains(event.SQL, "private") || (event.Err != nil && strings.Contains(event.Err.Error(), "private")) {
			t.Fatal("observer exposed a value")
		}
	}
	sentinel := errors.New("rollback")
	err = n.WithinTx(ctx, db, n.Postgres, nil, nil, func(tx n.Session) error {
		if err := tx.ExecOne(ctx, n.Update(r, note.Set("rolled back")).Where(tenant.EQ(7), id.EQ(1))); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	got, err = n.One(ctx, s, n.Select(r, p.Columns...).Where(tenant.EQ(7), id.EQ(1)), p)
	if err != nil {
		t.Fatal(err)
	}
	require(t, got.Note.Valid, false)
	if err = s.ExecOne(ctx, n.Delete(r).Where(tenant.EQ(7), id.EQ(1))); err != nil {
		t.Fatal(err)
	}
	t.Run("parent pagination and composite loaders", func(t *testing.T) {
		if _, err := n.One(ctx, s, insert, p); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO records(tenant,id,amount,payload,labels,moments,binary_values) SELECT 7,2,amount,payload,labels,moments,binary_values FROM records WHERE tenant=7 AND id=1;
INSERT INTO records(tenant,id,amount,payload,labels,moments,binary_values) SELECT 8,1,amount,payload,labels,moments,binary_values FROM records WHERE tenant=7 AND id=1;
CREATE TABLE children(tenant bigint NOT NULL,parent_id bigint NOT NULL,id bigint NOT NULL,PRIMARY KEY(tenant,id),FOREIGN KEY(tenant,parent_id) REFERENCES records(tenant,id));
INSERT INTO children VALUES(7,1,1),(7,1,2),(7,2,3),(8,1,1)`); err != nil {
			t.Fatal(err)
		}
		children := n.DefineTable(n.TableInfo{Name: "children", Columns: []n.ColumnInfo{{Name: "tenant"}, {Name: "parent_id"}, {Name: "id"}}, PrimaryKey: []string{"tenant", "id"}})
		childRef := children.As("c")
		ct := n.Field[int64](childRef, "tenant")
		cp := n.Field[int64](childRef, "parent_id")
		ci := n.Field[int64](childRef, "id")
		relation := n.Relation{From: table, To: children, FromColumns: []string{"tenant", "id"}, ToColumns: []string{"tenant", "parent_id"}}
		base := n.Select(r, p.Columns...).Where(tenant.EQ(7))
		page, err := n.All(ctx, s, base.Where(relation.Exists(r, childRef)).OrderBy(id.Asc()).Limit(1), p)
		if err != nil {
			t.Fatal(err)
		}
		require(t, len(page), 1)
		require(t, page[0].ID, int64(1))
		count, err := n.One(ctx, s, n.ParentCount(base.JoinRelation(relation, r, childRef, false).Limit(1)), n.CountProjection())
		if err != nil {
			t.Fatal(err)
		}
		require(t, count, int64(2))
		seek, err := n.All(ctx, s, base.SeekAfter(n.Cursor(tenant, int64(7), false), n.Cursor(id, int64(1), false)).Limit(1), p)
		if err != nil {
			t.Fatal(err)
		}
		require(t, len(seek), 1)
		require(t, seek[0].ID, int64(2))
		parents, err := n.All(ctx, s, base.OrderBy(id.Asc()), p)
		if err != nil {
			t.Fatal(err)
		}
		type child struct{ Tenant, Parent, ID int64 }
		type key struct{ Tenant, ID int64 }
		projection := n.Projection[child]{Columns: []n.Selection{ct.Select("tenant"), cp.Select("parent_id"), ci.Select("id")}, Scan: func(s n.Scanner) (child, error) {
			var v child
			err := s.Scan(&v.Tenant, &v.Parent, &v.ID)
			return v, err
		}}
		loader := n.BatchLoader[row, child, key]{ParentKey: func(v row) (key, bool) { return key{v.Tenant, v.ID}, true }, ChildKey: func(v child) key { return key{v.Tenant, v.Parent} }, Projection: projection, BatchSize: 1, MaxRows: 3, Query: func(keys []key) n.Buildable {
			return n.Select(childRef, projection.Columns...).Where(ct.EQ(keys[0].Tenant), cp.EQ(keys[0].ID)).OrderBy(ci.Asc())
		}}
		loaded, err := n.LoadMany(ctx, s, parents, loader)
		if err != nil {
			t.Fatal(err)
		}
		require(t, len(loaded[0].Related.Value), 2)
		require(t, len(loaded[1].Related.Value), 1)
		loader.MaxRows = 10
		loader.Query = func(keys []key) n.Buildable {
			return n.Select(childRef, projection.Columns...).Where(cp.EQ(keys[0].ID))
		}
		if _, err = n.LoadMany(ctx, s, parents, loader); err == nil {
			t.Fatal("cross-tenant child outside the composite-key batch accepted")
		}
	})
	t.Run("panic rolls back and reports failure", func(t *testing.T) {
		func() {
			defer func() {
				if reason := recover(); reason != sentinel {
					t.Fatalf("panic changed or disappeared: %v", reason)
				}
			}()
			_ = n.WithinTx(ctx, db, n.Postgres, nil, func(_ context.Context, event n.Event) { events = append(events, event) }, func(tx n.Session) error {
				if err := tx.ExecOne(ctx, n.Update(r, note.Set("panic-private")).Where(tenant.EQ(7), id.EQ(1))); err != nil {
					return err
				}
				return n.Each(ctx, tx, n.Select(r, p.Columns...).Where(tenant.EQ(7), id.EQ(1)), p, func(row) error { panic(sentinel) })
			})
		}()
		last := events[len(events)-1]
		if last.Err == nil || !last.InTransaction {
			t.Fatal("panic reported as a successful operation")
		}
		got, err := n.One(ctx, s, n.Select(r, p.Columns...).Where(tenant.EQ(7), id.EQ(1)), p)
		if err != nil {
			t.Fatal(err)
		}
		require(t, got.Note.Valid, false)
	})
	t.Run("long Unicode aliases remain distinct", func(t *testing.T) {
		prefix := strings.Repeat("路径_", 20)
		a := table.As(prefix + "left")
		b := table.As(prefix + "left")
		ai := n.Field[int64](a, "id")
		bi := n.Field[int64](b, "id")
		at := n.Field[int64](a, "tenant")
		bt := n.Field[int64](b, "tenant")
		type pair struct{ A, B int64 }
		projection := n.Projection[pair]{Columns: []n.Selection{ai.Select(prefix + "first"), bi.Select(prefix + "second")}, Scan: func(s n.Scanner) (pair, error) { var p pair; err := s.Scan(&p.A, &p.B); return p, err }}
		q := n.Select(a, projection.Columns...).JoinOn(b, n.And(n.Equal(ai.Expr(), bi.Expr()), n.Equal(at.Expr(), bt.Expr())), false).Where(at.EQ(7), ai.EQ(1))
		statement, err := q.Build(n.Postgres)
		if err != nil {
			t.Fatal(err)
		}
		for _, label := range statement.Columns {
			if len(label) > 63 || !utf8.ValidString(label) {
				t.Fatal("invalid bounded result label")
			}
		}
		if statement.Columns[0] == statement.Columns[1] {
			t.Fatal("labels collapsed")
		}
		got, err := n.One(ctx, s, q, projection)
		if err != nil {
			t.Fatal(err)
		}
		require(t, got, pair{1, 1})
		one := n.Projection[int64]{Columns: []n.Selection{ai.Select(prefix + "returning")}, Scan: func(s n.Scanner) (int64, error) { var v int64; err := s.Scan(&v); return v, err }}
		id, err := n.One(ctx, s, n.Update(a, n.Field[string](a, "note").AssignNullable(n.Null[string]())).Where(at.EQ(7), ai.EQ(1)).Returning(one.Columns...), one)
		if err != nil {
			t.Fatal(err)
		}
		require(t, id, int64(1))
	})
}
