package sqlite_test

import (
	"testing"

	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite"
)

func TestRelationMetadata(t *testing.T) {
	t.Parallel()

	relation := sqlite.NewRelation("main", "widgets", bob.ExpressionFunc(nil))
	if relation.Name() != "widgets" {
		t.Fatalf("unexpected name: %q", relation.Name())
	}
	if relation.Alias() != "main.widgets" {
		t.Fatalf("unexpected alias: %q", relation.Alias())
	}
	var _ bob.Expression = relation.Columns
	var _ bob.Expression = relation.NameExpr()
	var _ bob.Expression = relation.NameAsExpr()
}
