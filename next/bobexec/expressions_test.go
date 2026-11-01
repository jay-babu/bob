package bobexec_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/psql"
	"github.com/stephenafamo/bob/dialect/psql/sm"
	"github.com/stephenafamo/bob/next"
	"github.com/stephenafamo/bob/next/bobexec"
	"github.com/stephenafamo/bob/next/examples/models"
	"github.com/stephenafamo/bob/next/examples/models/relations/postsuser"
)

func TestMetadataExpressionsPreserveCompositeKeys(t *testing.T) {
	u, p := models.UsersAs("u"), models.PostsAs("p")
	query := psql.Select(
		sm.Columns(bobexec.Column(u.ID), bobexec.Column(p.Title)),
		sm.From(bobexec.Table(models.UsersTable)).As("u"),
		bobexec.LeftJoin(postsuser.Relation.Reverse(), "u", "p"),
		sm.Where(bobexec.Column(u.Tenant).EQ(psql.Arg(int64(7)))),
	)
	sql, args, err := bob.Build(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{`"u"."tenant" = "p"."tenant"`, `"u"."id" = "p"."user_id"`, `"u"."tenant" = $1`} {
		if !strings.Contains(sql, text) {
			t.Fatalf("composite relationship or parameter lost: %s", sql)
		}
	}
	if len(args) != 1 || args[0] != int64(7) {
		t.Fatal(args)
	}
	for _, relation := range []next.Relation{
		{},
		{From: models.UsersTable, To: models.PostsTable, FromColumns: []string{"id"}},
		{From: models.UsersTable, To: models.PostsTable, FromColumns: []string{"unknown"}, ToColumns: []string{"id"}},
	} {
		query := psql.Select(sm.Columns(psql.Raw("1")), sm.From(bobexec.Table(models.UsersTable)).As("u"), bobexec.LeftJoin(relation, "u", "p"))
		if _, _, err := bob.Build(context.Background(), query); err == nil {
			t.Fatal("invalid relationship metadata accepted")
		}
	}
}
