package psql

import (
	"github.com/jay-babu/bob"
	"github.com/jay-babu/bob/dialect/psql/dialect"
	"github.com/jay-babu/bob/expr"
)

func RawQuery(q string, args ...any) bob.BaseQuery[expr.Clause] {
	return expr.RawQuery(dialect.Dialect, q, args...)
}
