package next

import "fmt"

// JSONText extracts one PostgreSQL JSON object key as nullable SQL text. Keys
// remain parameters, and the operator retains expression-index compatibility.
func JSONText[T any](value Expr[T], key string) Expr[string] {
	return Expr[string]{jsonTextNode{value.node, key}}
}

type jsonTextNode struct {
	value node
	key   string
}

func (n jsonTextNode) write(r *renderer) {
	if r.dialect != Postgres {
		r.fail(fmt.Errorf("JSON text extraction requires PostgreSQL"))
		return
	}
	column, ok := n.value.(columnNode)
	if !ok || column.ref.Table() == nil {
		r.fail(fmt.Errorf("JSON text extraction requires a JSON column"))
		return
	}
	info, ok := column.ref.Table().column(column.name)
	if !ok || (info.DBType != "json" && info.DBType != "jsonb") {
		r.fail(fmt.Errorf("JSON text extraction requires a JSON column"))
		return
	}
	r.WriteByte('(')
	r.expr(n.value)
	r.WriteString(" ->> ")
	r.arg(n.key)
	r.WriteByte(')')
}
