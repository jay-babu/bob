package next_test

import (
	"strings"
	"testing"
	"time"

	n "github.com/stephenafamo/bob/next"
)

func TestCurrentTimestampComposesWithoutClientClock(t *testing.T) {
	table := n.DefineTable(n.TableInfo{Name: "entries", Columns: []n.ColumnInfo{{Name: "expires_at", DBType: "timestamp with time zone", Nullable: true}}})
	ref := table.As("e")
	expiry := n.Field[time.Time](ref, "expires_at")
	query := n.Select(ref, expiry.Select("expires_at")).Where(n.Or(expiry.IsNull(), n.Greater(expiry.Expr(), n.CurrentTimestamp())))
	for _, dialect := range []n.Dialect{n.Postgres, n.SQLite, n.MySQL} {
		statement := build(t, query, dialect)
		require(t, len(statement.Args), 0)
		require(t, strings.Contains(statement.SQL, "> CURRENT_TIMESTAMP"), true)
	}
}
