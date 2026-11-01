package next_test

import (
	"context"
	"testing"

	n "github.com/stephenafamo/bob/next"
	"github.com/stephenafamo/bob/next/internal/pgtest"
)

type relatedPerson struct {
	Tenant, ID int64
	Name       string
	Related    n.Relations
}
type relatedPost struct {
	Tenant, ID, UserID int64
	Title              string
	Related            n.Relations
}

func TestBoundedRelationshipPlansPreserveTenantAndParentPages(t *testing.T) {
	db := pgtest.Open(t)
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE people(tenant bigint,id bigint,name text,PRIMARY KEY(tenant,id));
CREATE TABLE posts(tenant bigint,id bigint,user_id bigint,title text,PRIMARY KEY(tenant,id),FOREIGN KEY(tenant,user_id) REFERENCES people(tenant,id));
INSERT INTO people VALUES(7,1,'first'),(7,2,'empty'),(8,1,'private');
INSERT INTO posts VALUES(7,1,1,'one'),(7,2,1,'two'),(8,1,1,'private');`); err != nil {
		t.Fatal(err)
	}
	people := n.DefineTable(n.TableInfo{Name: "people", PrimaryKey: []string{"tenant", "id"}, Columns: []n.ColumnInfo{{Name: "tenant", DBType: "bigint"}, {Name: "id", DBType: "bigint"}, {Name: "name", DBType: "text"}}})
	posts := n.DefineTable(n.TableInfo{Name: "posts", PrimaryKey: []string{"tenant", "id"}, Columns: []n.ColumnInfo{{Name: "tenant", DBType: "bigint"}, {Name: "id", DBType: "bigint"}, {Name: "user_id", DBType: "bigint"}, {Name: "title", DBType: "text"}}})
	personProjection := func(alias string) n.Projection[*relatedPerson] {
		r := people.As(alias)
		return n.Projection[*relatedPerson]{Columns: []n.Selection{n.Field[int64](r, "tenant").Select("tenant"), n.Field[int64](r, "id").Select("id"), n.Field[string](r, "name").Select("name")}, Scan: func(s n.Scanner) (*relatedPerson, error) {
			v := new(relatedPerson)
			err := s.Scan(&v.Tenant, &v.ID, &v.Name)
			return v, err
		}}
	}
	postProjection := func(alias string) n.Projection[*relatedPost] {
		r := posts.As(alias)
		return n.Projection[*relatedPost]{Columns: []n.Selection{n.Field[int64](r, "tenant").Select("tenant"), n.Field[int64](r, "id").Select("id"), n.Field[int64](r, "user_id").Select("user_id"), n.Field[string](r, "title").Select("title")}, Scan: func(s n.Scanner) (*relatedPost, error) {
			v := new(relatedPost)
			err := s.Scan(&v.Tenant, &v.ID, &v.UserID, &v.Title)
			return v, err
		}}
	}
	personDescriptor := n.Describe(people, personProjection, func(v *relatedPerson) []any { return []any{v.Tenant, v.ID, v.Name} }, func(v *relatedPerson) *n.Relations { return &v.Related })
	postDescriptor := n.Describe(posts, postProjection, func(v *relatedPost) []any { return []any{v.Tenant, v.ID, v.UserID, v.Title} }, func(v *relatedPost) *n.Relations { return &v.Related })
	relation := n.Relation{From: people, To: posts, FromColumns: []string{"tenant", "id"}, ToColumns: []string{"tenant", "user_id"}}
	plan := n.RelationPlan{Owner: personDescriptor, Steps: []n.RelationStep{{Name: "posts", Relation: relation, Target: postDescriptor, Many: true, Children: []n.RelationStep{{Name: "person", Relation: relation.Reverse(), Target: personDescriptor}}}}, BatchSize: 1, MaxRows: 10}
	var events []n.Event
	ctx := n.WithExecutor(context.Background(), db, n.ExecutionConfig{Observer: func(_ context.Context, event n.Event) { events = append(events, event) }})
	r := people.As("person")
	p := personProjection("person")
	// Use the same binding in the selection and its predicates.
	p.Columns = []n.Selection{n.Field[int64](r, "tenant").Select("tenant"), n.Field[int64](r, "id").Select("id"), n.Field[string](r, "name").Select("name")}
	base := n.FullRows(r, p).Where(n.Field[int64](r, "tenant").EQ(7)).OrderBy(n.Field[int64](r, "id").Asc())
	rows, err := base.WithRelations(plan).Limit(1).AllContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	require(t, len(rows), 1)
	children := n.RelatedMany[*relatedPost](&rows[0].Related, "posts")
	require(t, len(children), 2)
	for i, child := range children {
		require(t, child.Tenant, int64(7))
		require(t, child.ID, int64(i+1))
		parent := n.RelatedOne[*relatedPerson](&child.Related, "person")
		require(t, parent.Tenant, int64(7))
		require(t, parent.ID, int64(1))
	}
	custom, err := n.AllContext(ctx, n.SQL("SELECT tenant,id,name FROM people WHERE tenant=$1 AND id=$2", 7, 1), p)
	if err != nil {
		t.Fatal(err)
	}
	if err := n.LoadRelationsContext(ctx, plan, custom); err != nil {
		t.Fatal(err)
	}
	require(t, len(n.RelatedMany[*relatedPost](&custom[0].Related, "posts")), 2)
	// Parent paging stays independent of collection cardinality.
	rows, err = base.WithRelations(plan).Offset(1).Limit(1).AllContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	require(t, rows[0].ID, int64(2))
	require(t, rows[0].Related.IsLoaded("posts"), true)
	require(t, n.RelatedMany[*relatedPost](&rows[0].Related, "posts"), []*relatedPost{})
	require(t, rows[0].Related.IsLoaded("not_requested"), false)
	plan.MaxRows = 1
	if _, err := base.WithRelations(plan).Limit(1).AllContext(ctx); err == nil {
		t.Fatal("relationship row bound ignored")
	}
	if len(events) == 0 {
		t.Fatal("relationship execution bypassed observation")
	}
	resolver := n.RelationResolver(func(name string) (n.RelationStep, n.RelationResolver, bool) {
		return n.RelationStep{Name: name, Relation: relation, Target: postDescriptor, Many: true}, nil, name == "posts"
	})
	parsed, err := n.PlanRelations(personDescriptor, []string{"posts", "posts"}, resolver, nil)
	if err != nil {
		t.Fatal(err)
	}
	require(t, len(parsed.Steps), 1)
	for _, path := range []string{"missing", "posts.missing", "posts..bad", "a.b.c.d.e"} {
		if _, err := n.PlanRelations(personDescriptor, []string{path}, resolver, nil); err == nil {
			t.Fatal("invalid expansion accepted", path)
		}
	}
}
