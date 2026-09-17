package sqlite

import (
	"fmt"

	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/orm"
)

// Relation is the lightweight table metadata used by generated relationships.
type Relation[C bob.Expression] struct {
	schema string
	name   string
	alias  string

	Columns C
}

func NewRelation[C bob.Expression](schema, tableName string, columns C) *Relation[C] {
	alias := tableName
	if schema != "" {
		alias = fmt.Sprintf("%s.%s", schema, tableName)
	}

	return &Relation[C]{schema: schema, name: tableName, alias: alias, Columns: columns}
}

func (r *Relation[C]) NameExpr() Expression {
	if r.schema != "" {
		return Quote(r.schema, r.name)
	}

	return Expression{}.New(orm.SchemaTable(r.name))
}

func (r *Relation[C]) NameAsExpr() bob.Expression {
	expr := r.NameExpr()
	if r.schema != "" || r.alias != r.name {
		return expr.As(r.alias)
	}
	return expr
}

func (r *Relation[C]) Alias() string  { return r.alias }
func (r *Relation[C]) Schema() string { return r.schema }
func (r *Relation[C]) Name() string   { return r.name }
