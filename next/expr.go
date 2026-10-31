package next

import (
	"fmt"
	"strconv"
	"strings"
)

type Dialect uint8

const (
	Postgres Dialect = iota
	SQLite
	MySQL
)

type node interface{ write(*renderer) }
type Expr[T any] struct{ node node }
type Predicate struct{ node node }
type Selection struct {
	node  node
	label string
}
type Order struct {
	node node
	desc bool
}

func Value[T any](value T) Expr[T]                { return Expr[T]{valueNode{value}} }
func Equal[T any](a, b Expr[T]) Predicate         { return compare(a.node, "=", b.node) }
func compare(a node, op string, b node) Predicate { return Predicate{binaryNode{a, op, b}} }
func And(ps ...Predicate) Predicate               { return combine("AND", ps) }
func Or(ps ...Predicate) Predicate                { return combine("OR", ps) }
func combine(op string, ps []Predicate) Predicate {
	ns := make([]node, len(ps))
	for i, p := range ps {
		ns[i] = p.node
	}
	return Predicate{junctionNode{op, ns}}
}
func Not(p Predicate) Predicate                 { return Predicate{notNode{p.node}} }
func Exists(q SelectQuery) Predicate            { return Predicate{existsNode{q}} }
func LiteralOne() Selection                     { return Selection{literalNode("1"), ""} }
func Count() Expr[int64]                        { return Expr[int64]{literalNode("COUNT(*)")} }
func (e Expr[T]) Select(label string) Selection { return Selection{e.node, label} }
func (e Expr[T]) Asc() Order                    { return Order{e.node, false} }
func (e Expr[T]) Desc() Order                   { return Order{e.node, true} }
func (e Expr[T]) EQ(value T) Predicate          { return Equal(e, Value(value)) }
func (e Expr[T]) NE(value T) Predicate          { return compare(e.node, "<>", Value(value).node) }
func (e Expr[T]) GT(value T) Predicate          { return compare(e.node, ">", Value(value).node) }
func (e Expr[T]) GE(value T) Predicate          { return compare(e.node, ">=", Value(value).node) }
func (e Expr[T]) LT(value T) Predicate          { return compare(e.node, "<", Value(value).node) }
func (e Expr[T]) LE(value T) Predicate          { return compare(e.node, "<=", Value(value).node) }

// Like is restricted to string expressions; callers choose wildcard escaping.
func Like[T ~string](value Expr[T], pattern T) Predicate {
	return compare(value.node, "LIKE", Value(pattern).node)
}

type Number interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~float32 | ~float64
}

func Add[T Number](a, b Expr[T]) Expr[T] { return Expr[T]{binaryNode{a.node, "+", b.node}} }

type renderer struct {
	strings.Builder
	dialect  Dialect
	args     []any
	scope    map[*binding]string
	err      error
	excluded *Table
}

func (r *renderer) fail(err error) {
	if r.err == nil {
		r.err = err
	}
}
func (r *renderer) ident(s string) {
	quote := `"`
	if r.dialect == MySQL {
		quote = "`"
	}
	r.WriteString(quote)
	r.WriteString(strings.ReplaceAll(s, quote, quote+quote))
	r.WriteString(quote)
}
func (r *renderer) arg(v any) {
	r.args = append(r.args, v)
	if r.dialect == Postgres {
		r.WriteByte('$')
		r.WriteString(strconv.Itoa(len(r.args)))
	} else {
		r.WriteByte('?')
	}
}
func (r *renderer) expr(n node) {
	if n == nil {
		r.fail(fmt.Errorf("empty expression"))
		return
	}
	n.write(r)
}
func (r *renderer) ref(ref Ref) {
	if ref.b == nil || ref.b.table == nil {
		r.fail(fmt.Errorf("empty table reference"))
		return
	}
	if ref.b.table.err != nil {
		r.fail(ref.b.table.err)
		return
	}
	i := ref.b.table.info
	if i.Schema != "" {
		r.ident(i.Schema)
		r.WriteByte('.')
	}
	r.ident(i.Name)
	r.WriteString(" AS ")
	r.ident(ref.b.alias)
}

type columnNode struct {
	ref  Ref
	name string
}

func (n columnNode) write(r *renderer) {
	alias, ok := r.scope[n.ref.b]
	if !ok {
		r.fail(fmt.Errorf("column %s references a table outside query scope", n.name))
		return
	}
	if _, ok := n.ref.b.table.column(n.name); !ok {
		r.fail(fmt.Errorf("unknown column %s", n.name))
		return
	}
	r.ident(alias)
	r.WriteByte('.')
	r.ident(n.name)
}

type valueNode struct{ value any }

func (n valueNode) write(r *renderer) { r.arg(n.value) }

type literalNode string

func (n literalNode) write(r *renderer) { r.WriteString(string(n)) }

type errorNode struct{ err error }

func (n errorNode) write(r *renderer) { r.fail(n.err) }

type binaryNode struct {
	a  node
	op string
	b  node
}

func (n binaryNode) write(r *renderer) {
	r.WriteByte('(')
	r.expr(n.a)
	r.WriteByte(' ')
	r.WriteString(n.op)
	r.WriteByte(' ')
	r.expr(n.b)
	r.WriteByte(')')
}

type unaryNode struct {
	a  node
	op string
}

func (n unaryNode) write(r *renderer) {
	r.WriteByte('(')
	r.expr(n.a)
	r.WriteByte(' ')
	r.WriteString(n.op)
	r.WriteByte(')')
}

type notNode struct{ a node }

func (n notNode) write(r *renderer) { r.WriteString("NOT ("); r.expr(n.a); r.WriteByte(')') }

type junctionNode struct {
	op    string
	nodes []node
}

func (n junctionNode) write(r *renderer) {
	if len(n.nodes) == 0 {
		if n.op == "AND" {
			r.WriteString("TRUE")
		} else {
			r.WriteString("FALSE")
		}
		return
	}
	r.WriteByte('(')
	for i, e := range n.nodes {
		if i > 0 {
			r.WriteByte(' ')
			r.WriteString(n.op)
			r.WriteByte(' ')
		}
		r.expr(e)
	}
	r.WriteByte(')')
}

type inNode struct {
	a      node
	values []node
}

func (n inNode) write(r *renderer) {
	if len(n.values) == 0 {
		r.WriteString("FALSE")
		return
	}
	r.WriteByte('(')
	r.expr(n.a)
	r.WriteString(" IN (")
	for i, e := range n.values {
		if i > 0 {
			r.WriteString(", ")
		}
		r.expr(e)
	}
	r.WriteString("))")
}

type existsNode struct{ q SelectQuery }

func (n existsNode) write(r *renderer) { r.WriteString("EXISTS ("); n.q.write(r); r.WriteByte(')') }
