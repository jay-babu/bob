package vm

import (
	"github.com/jay-babu/bob"
	"github.com/jay-babu/bob/dialect/sqlite/dialect"
	"github.com/jay-babu/bob/mods"
)

func RowValue(clauses ...bob.Expression) bob.Mod[*dialect.ValuesQuery] {
	return mods.Values[*dialect.ValuesQuery](clauses)
}
