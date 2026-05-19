package mods

import (
	"github.com/jay-babu/bob"
	"github.com/jay-babu/bob/clause"
)

var (
	_ bob.Mod[any]                                = QueryMods[any](nil)
	_ bob.Mod[interface{ AppendCTE(clause.CTE) }] = With[interface{ AppendCTE(clause.CTE) }]{}
)
