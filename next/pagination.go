package next

import "fmt"

// CursorKey preserves each cursor value's Go type before erasing it into the
// shared query representation. Nullable keys require an explicit null policy.
type CursorKey struct {
	column     columnNode
	value      node
	descending bool
}

func Cursor[T any](column Column[T], value T, descending bool) CursorKey {
	return CursorKey{columnNode{column.ref, column.name}, valueNode{value}, descending}
}

// SeekAfter creates lexicographic predicates and matching ordering. Keys must
// include a nonnullable unique key, avoiding unstable pages when sort values tie.
func (q SelectQuery) SeekAfter(keys ...CursorKey) SelectQuery {
	if len(keys) == 0 || len(q.order) > 0 || q.offset != nil {
		q.err = fmt.Errorf("seek requires keys, no prior ordering, and no offset")
		return q
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if q.from.b == nil || q.from.b.table == nil || k.column.ref.b != q.from.b || seen[k.column.name] {
			q.err = fmt.Errorf("seek keys must be distinct columns of the parent table")
			return q
		}
		info, ok := q.from.b.table.column(k.column.name)
		if !ok || info.Nullable {
			q.err = fmt.Errorf("seek keys must be known and nonnullable")
			return q
		}
		seen[k.column.name] = true
	}
	unique := false
	info := q.from.b.table.info
	for _, key := range append([][]string{info.PrimaryKey}, info.UniqueKeys...) {
		contains := len(key) > 0
		for _, column := range key {
			contains = contains && seen[column]
		}
		unique = unique || contains
	}
	if !unique {
		q.err = fmt.Errorf("seek ordering must include a unique parent key")
		return q
	}
	terms := make([]Predicate, len(keys))
	prefix := []Predicate{}
	for i, k := range keys {
		op := ">"
		if k.descending {
			op = "<"
		}
		terms[i] = And(append(append([]Predicate(nil), prefix...), compare(k.column, op, k.value))...)
		prefix = append(prefix, compare(k.column, "=", k.value))
		q.order = append(q.order, Order{k.column, k.descending})
	}
	return q.Where(Or(terms...))
}
