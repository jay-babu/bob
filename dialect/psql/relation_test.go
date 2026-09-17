package psql_test

import (
	"testing"

	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/psql"
)

func TestRelationMetadata(t *testing.T) {
	t.Parallel()

	relation := psql.NewRelation("public", "widgets", bob.ExpressionFunc(nil))
	if relation.Name() != "widgets" {
		t.Fatalf("unexpected name: %q", relation.Name())
	}
	if relation.Alias() != "public.widgets" {
		t.Fatalf("unexpected alias: %q", relation.Alias())
	}
	var _ bob.Expression = relation.Columns
	var _ bob.Expression = relation.NameExpr()
	var _ bob.Expression = relation.NameAsExpr()
}
