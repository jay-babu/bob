package mysql_test

import (
	"testing"

	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/mysql"
)

func TestRelationMetadata(t *testing.T) {
	t.Parallel()

	relation := mysql.NewRelation("widgets", bob.ExpressionFunc(nil))
	if relation.Name() != "widgets" {
		t.Fatalf("unexpected name: %q", relation.Name())
	}
	if relation.Alias() != "widgets" {
		t.Fatalf("unexpected alias: %q", relation.Alias())
	}
	var _ bob.Expression = relation.Columns
	var _ bob.Expression = relation.NameExpr()
}
