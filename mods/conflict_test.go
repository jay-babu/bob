package mods

import (
	"github.com/jay-babu/bob"
)

var _ bob.Mod[interface{ SetConflict(bob.Expression) }] = Conflict[interface{ SetConflict(bob.Expression) }](nil)
