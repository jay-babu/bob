package next_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	n "github.com/stephenafamo/bob/next"
	"github.com/stephenafamo/bob/next/examples/models/posts"
	"github.com/stephenafamo/bob/next/examples/models/relations/postsuser"
	"github.com/stephenafamo/bob/next/examples/models/users"
	"github.com/stephenafamo/bob/next/gen"
)

func TestGeneratedModelsEndToEnd(t *testing.T) {
	db := database(t)
	ctx := context.Background()
	s := n.NewSession(db, n.SQLite, nil)
	u := users.As("u")
	p := u.Projection()
	input := users.Insert{ID: 3, Tenant: 7, Name: "Charlie", Note: n.Nullable(n.Set("memo"))}
	row, err := n.One(ctx, s, n.Insert(u.Ref, input.Assignments(u)...).Returning(p.Columns...), p)
	if err != nil {
		t.Fatal(err)
	}
	require(t, row, users.Row{ID: 3, Tenant: 7, Name: "Charlie", Note: sql.Null[string]{V: "memo", Valid: true}, Version: 1, Fingerprint: 6})
	update := users.Update{Name: n.Set("Changed"), Note: n.Null[string](), Version: n.SetExpr(n.Add(u.Version.Expr(), n.Value(int64(1))))}
	if err = s.ExecOne(ctx, n.Update(u.Ref, update.Assignments(u)...).Where(u.Tenant.EQ(7), u.ID.EQ(3), u.Version.EQ(1))); err != nil {
		t.Fatal(err)
	}
	if err = s.ExecOne(ctx, n.Update(u.Ref, update.Assignments(u)...).Where(u.Tenant.EQ(7), u.ID.EQ(3), u.Version.EQ(1))); !errors.Is(err, n.ErrNotMatched) {
		t.Fatal(err)
	}
	row, err = n.One(ctx, s, n.Select(u.Ref, p.Columns...).Where(u.Tenant.EQ(7), u.ID.EQ(3)), p)
	if err != nil {
		t.Fatal(err)
	}
	require(t, row.Note.Valid, false)
	require(t, row.Version, int64(2))
	post := posts.As("p")
	rows, err := n.All(ctx, s, n.Select(u.Ref, p.Columns...).Where(u.Tenant.EQ(7), postsuser.Relation.Reverse().Exists(u.Ref, post.Ref)).OrderBy(u.ID.Asc()).Limit(1), p)
	if err != nil {
		t.Fatal(err)
	}
	require(t, len(rows), 1)
	require(t, rows[0].ID, int64(1))
	// The conflict target is the composite tenant/id key, not id alone.
	upsert := n.Insert(u.Ref, input.Assignments(u)...).OnConflict(u.Tenant.Select(""), u.ID.Select("")).DoUpdate(u.Name.SetExpr(n.Excluded(u.Name))).Returning(p.Columns...)
	row, err = n.One(ctx, s, upsert, p)
	if err != nil {
		t.Fatal(err)
	}
	require(t, row.Name, "Charlie")
	require(t, row.Version, int64(2))
	if _, err = upsert.Build(n.MySQL); err == nil {
		t.Fatal("MySQL conflict/returning accepted")
	}
	if _, err = n.Update(u.Ref, u.Name.SetExpr(n.Excluded(u.Name))).Where(u.ID.EQ(3)).Build(n.Postgres); err == nil {
		t.Fatal("excluded accepted outside upsert")
	}
	if _, err = n.Insert(u.Ref, input.Assignments(u)...).OnConflict(u.ID.Select("")).DoUpdate(u.Name.Set("bad")).Build(n.Postgres); err == nil {
		t.Fatal("nonunique conflict target accepted")
	}
}

func TestParentCountIgnoresCollectionMultiplicity(t *testing.T) {
	db := database(t)
	s := n.NewSession(db, n.SQLite, nil)
	u := users.As("u")
	p := posts.As("p")
	q := n.Select(u.Ref, u.ID.Select("id")).JoinRelation(postsuser.Relation.Reverse(), u.Ref, p.Ref, false).Where(u.Tenant.EQ(7)).Limit(1)
	count, err := n.One(context.Background(), s, n.ParentCount(q), n.CountProjection())
	if err != nil {
		t.Fatal(err)
	}
	require(t, count, int64(2))
}

func TestCheckedInGeneratedModelsAreCurrent(t *testing.T) {
	data, err := os.ReadFile("examples/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema gen.Schema
	if err = json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err = gen.Generate(schema, gen.Config{Directory: dir, ImportPath: "github.com/stephenafamo/bob/next/examples/models", Relations: []string{"postsuser"}}); err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		actual, err := os.ReadFile(filepath.Join("examples/models", rel))
		if err != nil {
			return err
		}
		expected, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if string(actual) != string(expected) {
			t.Errorf("generated example %s is stale", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestKeysetPaginationTiesAndScope(t *testing.T) {
	db := database(t)
	ctx := context.Background()
	s := n.NewSession(db, n.SQLite, nil)
	u := users.As("u")
	p := u.Projection()
	first := n.Select(u.Ref, p.Columns...).SeekAfter(n.Cursor(u.Tenant, int64(7), false), n.Cursor(u.ID, int64(1), false)).Limit(10)
	rows, err := n.All(ctx, s, first, p)
	if err != nil {
		t.Fatal(err)
	}
	require(t, len(rows), 2)
	require(t, rows[0].Name, "Bob")
	require(t, rows[1].Name, "Other")
	backward := n.Select(u.Ref, p.Columns...).SeekAfter(n.Cursor(u.Tenant, int64(8), true), n.Cursor(u.ID, int64(1), true)).Limit(10)
	rows, err = n.All(ctx, s, backward, p)
	if err != nil {
		t.Fatal(err)
	}
	require(t, len(rows), 2)
	require(t, rows[0].Name, "Bob")
	require(t, rows[1].Name, "Alice")
	for _, q := range []n.SelectQuery{
		n.Select(u.Ref, p.Columns...).SeekAfter(n.Cursor(u.Name, "Alice", false)),
		n.Select(u.Ref, p.Columns...).SeekAfter(n.Cursor(u.Note, "memo", false), n.Cursor(u.Tenant, int64(7), false), n.Cursor(u.ID, int64(1), false)),
		n.Select(u.Ref, p.Columns...).OrderBy(u.ID.Asc()).SeekAfter(n.Cursor(u.Tenant, int64(7), false), n.Cursor(u.ID, int64(1), false)),
	} {
		if _, err := q.Build(n.Postgres); err == nil {
			t.Fatal("unstable or ambiguous cursor accepted")
		}
	}
}
