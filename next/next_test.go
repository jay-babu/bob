package next_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	n "github.com/stephenafamo/bob/next"
	_ "modernc.org/sqlite"
)

func fixture() (*n.Table, *n.Table, n.Relation) {
	u := n.DefineTable(n.TableInfo{Name: "users", Columns: []n.ColumnInfo{{Name: "id"}, {Name: "tenant"}, {Name: "name"}, {Name: "note", Nullable: true}, {Name: "version", HasDefault: true}}, PrimaryKey: []string{"tenant", "id"}})
	p := n.DefineTable(n.TableInfo{Name: "posts", Columns: []n.ColumnInfo{{Name: "id"}, {Name: "tenant"}, {Name: "user_id"}, {Name: "title"}}, PrimaryKey: []string{"tenant", "id"}})
	return u, p, n.Relation{From: u, To: p, FromColumns: []string{"tenant", "id"}, ToColumns: []string{"tenant", "user_id"}}
}
func require[T any](t *testing.T, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
}
func build(t *testing.T, q n.Buildable, d n.Dialect) n.Statement {
	t.Helper()
	s, e := q.Build(d)
	if e != nil {
		t.Fatal(e)
	}
	return s
}

func TestCompositionBindingsAndImmutability(t *testing.T) {
	u, posts, rel := fixture()
	a := u.As("u")
	p := posts.As("p")
	id := n.Field[int64](a, "id")
	tenant := n.Field[int64](a, "tenant")
	base := n.Select(a, id.Select("id")).Where(tenant.EQ(7))
	one := base.Where(id.EQ(1))
	two := base.Where(id.EQ(2))
	require(t, build(t, one, n.Postgres).Args, []any{int64(7), int64(1)})
	require(t, build(t, two, n.Postgres).Args, []any{int64(7), int64(2)})
	q := base.Where(rel.Exists(a, p, n.Field[string](p, "title").EQ("secret"))).OrderBy(id.Asc()).Limit(5)
	s := build(t, q, n.Postgres)
	require(t, s.Args, []any{int64(7), "secret", 5})
	if !strings.Contains(s.SQL, `"u"."tenant" = "p"."tenant"`) || !strings.Contains(s.SQL, "EXISTS") || !strings.Contains(s.SQL, "LIMIT $3") {
		t.Fatal(s.SQL)
	}
	for _, d := range []n.Dialect{n.SQLite, n.MySQL} {
		require(t, build(t, q, d).Args, s.Args)
	}
	if _, err := base.Where(n.Field[int64](u.As("other"), "id").EQ(1)).Build(n.Postgres); err == nil {
		t.Fatal("out-of-scope column accepted")
	}
	shadowed := build(t, base.Where(rel.Exists(a, posts.As("u"))), n.Postgres)
	if !strings.Contains(shadowed.SQL, `AS "u_2"`) {
		t.Fatal("independent bindings did not receive distinct SQL aliases")
	}
	build(t, base.JoinOn(posts.As("u"), id.EQ(1), false), n.Postgres)
	if _, err := base.JoinOn(a, id.EQ(1), false).Build(n.Postgres); err == nil {
		t.Fatal("one binding used as two distinct table occurrences")
	}
	if _, err := base.Where(n.Field[int](a, "missing").EQ(1)).Build(n.Postgres); err == nil {
		t.Fatal("unknown column accepted")
	}
}

func TestJoinPaginationCardinality(t *testing.T) {
	u, posts, rel := fixture()
	a := u.As("u")
	p := posts.As("p")
	parents := n.Select(a, n.Field[int64](a, "id").Select("id"))
	if _, err := parents.JoinRelation(rel, a, p, true).Limit(10).Build(n.Postgres); err == nil {
		t.Fatal("multiplying pagination accepted")
	}
	children := n.Select(p, n.Field[int64](p, "id").Select("id"))
	build(t, children.JoinRelation(rel.Reverse(), p, a, true).Limit(10), n.Postgres)
	build(t, parents.Where(rel.Exists(a, p)).Limit(10), n.Postgres)
	if _, err := parents.Offset(10).Build(n.MySQL); err == nil {
		t.Fatal("offset without limit accepted")
	}
	if _, err := parents.Limit(-1).Build(n.Postgres); err == nil {
		t.Fatal("negative limit accepted")
	}
	// ON cannot refer to a join that appears later in SQL.
	p2 := posts.As("p2")
	if _, err := parents.JoinOn(p, n.Equal(n.Field[int](p2, "id").Expr(), n.Field[int](a, "id").Expr()), false).JoinOn(p2, n.Field[int](p2, "id").EQ(1), false).Build(n.Postgres); err == nil {
		t.Fatal("forward ON reference accepted")
	}
}

func TestWriteSemantics(t *testing.T) {
	u, _, _ := fixture()
	r := u.As("u")
	id := n.Field[int64](r, "id")
	tenant := n.Field[int64](r, "tenant")
	name := n.Field[string](r, "name")
	note := n.Field[string](r, "note")
	version := n.Field[int](r, "version")
	q := n.Insert(r, id.Set(1), tenant.Set(7), name.Set(""), note.AssignNullable(n.Null[string]()), version.Assign(n.Default[int]())).Returning(id.Select("id"))
	s := build(t, q, n.Postgres)
	require(t, s.Args, []any{int64(1), int64(7), ""})
	if !strings.Contains(s.SQL, "NULL") {
		t.Fatal(s.SQL)
	}
	for _, q := range []n.Buildable{n.Insert(r, id.Set(1)), n.Update(r, name.Set("x")), n.Delete(r), n.Update(r, name.Set("x"), name.Set("y")).Where(id.EQ(1)), n.Insert(r, id.Set(1), tenant.Set(7), name.AssignNullable(n.Null[string]()))} {
		if _, err := q.Build(n.Postgres); err == nil {
			t.Fatal("invalid write accepted")
		}
	}
	build(t, n.Update(r, name.Set("x")).Where(n.And(id.EQ(1), tenant.EQ(7), version.EQ(2))), n.Postgres)
	build(t, n.Delete(r).AllRows(), n.SQLite)
	if _, err := q.Build(n.MySQL); err == nil {
		t.Fatal("MySQL returning accepted")
	}
}

func database(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, s := range []string{
		`CREATE TABLE users (id INTEGER NOT NULL, tenant INTEGER NOT NULL, name TEXT NOT NULL, note TEXT, version INTEGER NOT NULL DEFAULT 1, fingerprint INTEGER GENERATED ALWAYS AS (id * 2) STORED, PRIMARY KEY(tenant,id))`,
		`CREATE TABLE posts (id INTEGER NOT NULL, tenant INTEGER NOT NULL, user_id INTEGER NOT NULL, title TEXT NOT NULL, PRIMARY KEY(tenant,id))`,
		`INSERT INTO users(id,tenant,name) VALUES (1,7,'Alice'),(2,7,'Bob'),(1,8,'Other')`,
		`INSERT INTO posts VALUES (1,7,1,'first'),(2,7,1,'second'),(3,7,2,'third'),(1,8,1,'private')`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

type user struct {
	ID, Tenant int64
	Name       string
}

func userProjection(r n.Ref) n.Projection[user] {
	return n.Projection[user]{Columns: []n.Selection{n.Field[int64](r, "id").Select("id"), n.Field[int64](r, "tenant").Select("tenant"), n.Field[string](r, "name").Select("name")}, Scan: func(s n.Scanner) (user, error) { var v user; err := s.Scan(&v.ID, &v.Tenant, &v.Name); return v, err }}
}

func TestDatabaseProjectionTransactionsAndObservation(t *testing.T) {
	db := database(t)
	ctx := context.Background()
	u, _, _ := fixture()
	r := u.As("u")
	p := userProjection(r)
	var events []n.Event
	observe := func(_ context.Context, e n.Event) { events = append(events, e) }
	s := n.NewSession(db, n.SQLite, observe)
	q := n.Select(r, p.Columns...).Where(n.Field[int64](r, "tenant").EQ(7)).OrderBy(n.Field[int64](r, "id").Asc())
	got, err := n.All(ctx, s, q, p)
	if err != nil {
		t.Fatal(err)
	}
	require(t, got, []user{{1, 7, "Alice"}, {2, 7, "Bob"}})
	_, err = n.All(ctx, s, n.Select(r, n.Field[int64](r, "id").Select("id")), p)
	if err == nil {
		t.Fatal("partial row scanned into complete destination")
	}
	sentinel := errors.New("rollback")
	err = n.WithinTx(ctx, db, n.SQLite, nil, observe, func(tx n.Session) error {
		if err := tx.ExecOne(ctx, n.Update(r, n.Field[string](r, "name").Set("secret-value")).Where(n.Field[int64](r, "tenant").EQ(7), n.Field[int64](r, "id").EQ(1))); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	got, err = n.All(ctx, s, q, p)
	if err != nil {
		t.Fatal(err)
	}
	require(t, got[0].Name, "Alice")
	for _, event := range events {
		if strings.Contains(fmt.Sprint(event), "secret-value") {
			t.Fatal("argument value exposed")
		}
	}
	if !events[len(events)-2].InTransaction {
		t.Fatal("transaction not observed")
	}
	err = s.ExecOne(ctx, n.Update(r, n.Field[string](r, "name").Set("never")).Where(n.Field[int64](r, "id").EQ(999)))
	if !errors.Is(err, n.ErrNotMatched) {
		t.Fatal(err)
	}
	// A failed consumer must close rows and release the sole connection.
	err = n.Each(ctx, s, q, p, func(user) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if _, err = n.All(ctx, s, q, p); err != nil {
		t.Fatal(err)
	}
	_, err = n.One(ctx, s, q, p)
	if err == nil {
		t.Fatal("One accepted multiple rows")
	}
	_, err = n.One(ctx, s, q.Where(n.Field[int64](r, "id").EQ(99)), p)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
}

type post struct {
	ID, Tenant, UserID int64
	Title              string
}
type key struct{ Tenant, ID int64 }

func TestBatchLoadingCompositeKeysAndBounds(t *testing.T) {
	db := database(t)
	ctx := context.Background()
	u, posts, _ := fixture()
	r := u.As("u")
	pr := posts.As("p")
	p := userProjection(r)
	queryCount := 0
	s := n.NewSession(db, n.SQLite, func(context.Context, n.Event) { queryCount++ })
	parents, err := n.All(ctx, s, n.Select(r, p.Columns...).Where(n.Field[int64](r, "tenant").EQ(7)).OrderBy(n.Field[int64](r, "id").Asc()).Limit(2), p)
	if err != nil {
		t.Fatal(err)
	}
	parents = append(parents, parents[0])
	parents = append(parents, user{99, 7, "Absent"})
	projection := n.Projection[post]{Columns: []n.Selection{n.Field[int64](pr, "id").Select("id"), n.Field[int64](pr, "tenant").Select("tenant"), n.Field[int64](pr, "user_id").Select("user_id"), n.Field[string](pr, "title").Select("title")}, Scan: func(s n.Scanner) (post, error) {
		var v post
		err := s.Scan(&v.ID, &v.Tenant, &v.UserID, &v.Title)
		return v, err
	}}
	loader := n.BatchLoader[user, post, key]{ParentKey: func(u user) (key, bool) { return key{u.Tenant, u.ID}, true }, ChildKey: func(p post) key { return key{p.Tenant, p.UserID} }, Projection: projection, BatchSize: 2, MaxRows: 10, Query: func(keys []key) n.Buildable {
		ps := make([]n.Predicate, len(keys))
		for i, k := range keys {
			ps[i] = n.And(n.Field[int64](pr, "tenant").EQ(k.Tenant), n.Field[int64](pr, "user_id").EQ(k.ID))
		}
		return n.Select(pr, projection.Columns...).Where(n.Or(ps...)).OrderBy(n.Field[int64](pr, "id").Asc())
	}}
	loaded, err := n.LoadMany(ctx, s, parents, loader)
	if err != nil {
		t.Fatal(err)
	}
	require(t, queryCount, 3)
	require(t, len(loaded[0].Related.Value), 2)
	require(t, len(loaded[1].Related.Value), 1)
	require(t, len(loaded[3].Related.Value), 0)
	if !loaded[3].Related.IsLoaded {
		t.Fatal("absent relationship not marked loaded")
	}
	loaded[0].Related.Value[0].Title = "changed"
	require(t, loaded[2].Related.Value[0].Title, "first")
	loader.MaxRows = 1
	if _, err = n.LoadMany(ctx, s, parents, loader); err == nil {
		t.Fatal("row bound ignored")
	}
	loader.MaxRows = 10
	if _, err = n.LoadOne(ctx, s, parents, loader); err == nil {
		t.Fatal("to-one cardinality ignored")
	}
	loader.BatchSize = 0
	if _, err = n.LoadMany(ctx, s, parents, loader); err == nil {
		t.Fatal("missing bounds accepted")
	}
}

func TestMetadataCopiesAndIdentifierQuoting(t *testing.T) {
	u, _, _ := fixture()
	info := u.Info()
	info.Columns[0].Name = "bad"
	info.PrimaryKey[0] = "bad"
	require(t, u.Info().Columns[0].Name, "id")
	require(t, u.Info().PrimaryKey[0], "tenant")
	table := n.DefineTable(n.TableInfo{Name: `ta"ble`, Columns: []n.ColumnInfo{{Name: `co"l`}}})
	r := table.As(`a"b`)
	s := build(t, n.Select(r, n.Field[int](r, `co"l`).Select("value")), n.Postgres)
	if !strings.Contains(s.SQL, `"ta""ble"`) || !strings.Contains(s.SQL, `"a""b"."co""l"`) {
		t.Fatal(s.SQL)
	}
}

func BenchmarkBuild(b *testing.B) {
	u, posts, rel := fixture()
	r := u.As("u")
	p := posts.As("p")
	q := n.Select(r, n.Field[int64](r, "id").Select("id")).Where(n.Field[int64](r, "tenant").EQ(7), rel.Exists(r, p)).Limit(100)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := q.Build(n.Postgres); err != nil {
			b.Fatal(err)
		}
	}
}
