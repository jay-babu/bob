package mysql

import "github.com/stephenafamo/bob"

// Relation is the lightweight table metadata used by generated relationships.
type Relation[C bob.Expression] struct {
	name  string
	alias string

	Columns C
}

func NewRelation[C bob.Expression](tableName string, columns C) *Relation[C] {
	return &Relation[C]{name: tableName, alias: tableName, Columns: columns}
}

func (r *Relation[C]) NameExpr() Expression { return Quote(r.name) }

func (r *Relation[C]) NameAsExpr() bob.Expression {
	expr := r.NameExpr()
	if r.alias != r.name {
		return expr.As(r.alias)
	}
	return expr
}

func (r *Relation[C]) Alias() string { return r.alias }
func (r *Relation[C]) Name() string  { return r.name }
