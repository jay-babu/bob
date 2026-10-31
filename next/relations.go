package next

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// Relations stores requested relationship results without references to model
// types in a row's field layout. Accessing it never performs database work.
type Relations struct{ values map[string]relatedValue }
type relatedValue struct {
	value any
	many  bool
}

func (r *Relations) IsLoaded(name string) bool {
	if r == nil {
		return false
	}
	_, ok := r.values[name]
	return ok
}

func RelatedOne[T any](r *Relations, name string) T {
	var zero T
	if !r.IsLoaded(name) {
		return zero
	}
	v := r.values[name]
	if v.many {
		panic("to-many relationship accessed as to-one")
	}
	if v.value == nil {
		return zero
	}
	return v.value.(T)
}

func RelatedMany[T any](r *Relations, name string) []T {
	if !r.IsLoaded(name) {
		return nil
	}
	v := r.values[name]
	if !v.many {
		panic("to-one relationship accessed as to-many")
	}
	values := v.value.([]any)
	result := make([]T, len(values))
	for i, value := range values {
		result[i] = value.(T)
	}
	return result
}

func SetRelatedOne[T any](r *Relations, name string, value T) {
	r.set(name, relatedValue{value: value})
}

func SetRelatedMany[T any](r *Relations, name string, values []T) {
	erased := make([]any, len(values))
	for i, value := range values {
		erased[i] = value
	}
	r.set(name, relatedValue{value: erased, many: true})
}

func (r *Relations) set(name string, value relatedValue) {
	if r.values == nil {
		r.values = map[string]relatedValue{}
	}
	r.values[name] = value
}

// RowDescriptor is constructed by generated bindings. The shared loader needs
// no reflection or per-relationship execution/scanner implementation.
type RowDescriptor struct {
	Table  *Table
	bind   func(string) Projection[any]
	values func(any) []any
	state  func(any) *Relations
}

func Describe[T any](table *Table, bind func(string) Projection[T], values func(T) []any, state func(T) *Relations) RowDescriptor {
	if bind == nil || values == nil || state == nil {
		return RowDescriptor{Table: table}
	}
	return RowDescriptor{Table: table, bind: func(alias string) Projection[any] {
		p := bind(alias)
		return Projection[any]{Columns: p.Columns, Scan: func(s Scanner) (any, error) { return p.Scan(s) }}
	}, values: func(row any) []any { return values(row.(T)) }, state: func(row any) *Relations { return state(row.(T)) }}
}

type RelationStep struct {
	Name     string
	Relation Relation
	Target   RowDescriptor
	Many     bool
	Children []RelationStep
}

type RelationPlan struct {
	Owner     RowDescriptor
	Steps     []RelationStep
	BatchSize int
	MaxRows   int
}

type RelationResolver func(string) (RelationStep, RelationResolver, bool)

// PlanRelations resolves only explicitly requested paths. Computed terminals
// are validated here and loaded by application-owned code after row retrieval.
func PlanRelations(owner RowDescriptor, paths []string, resolver RelationResolver, computed func(string) bool) (RelationPlan, error) {
	type tree map[string]tree
	root := tree{}
	for _, path := range paths {
		tokens := strings.Split(path, ".")
		if len(tokens) > 4 {
			return RelationPlan{}, fmt.Errorf("relationship expansion exceeds four levels")
		}
		node := root
		for _, token := range tokens {
			if token == "" || strings.TrimSpace(token) != token {
				return RelationPlan{}, fmt.Errorf("invalid relationship path %q", path)
			}
			if node[token] == nil {
				node[token] = tree{}
			}
			node = node[token]
		}
	}
	var walk func(tree, RelationResolver, string) ([]RelationStep, error)
	walk = func(node tree, resolve RelationResolver, prefix string) ([]RelationStep, error) {
		names := make([]string, 0, len(node))
		for name := range node {
			names = append(names, name)
		}
		slices.Sort(names)
		steps := make([]RelationStep, 0, len(names))
		for _, name := range names {
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			if computed != nil && computed(path) && len(node[name]) == 0 {
				continue
			}
			if resolve == nil {
				return nil, fmt.Errorf("unknown relationship path %s", path)
			}
			step, child, ok := resolve(name)
			if !ok {
				return nil, fmt.Errorf("unknown relationship path %s", path)
			}
			var err error
			step.Children, err = walk(node[name], child, path)
			if err != nil {
				return nil, err
			}
			steps = append(steps, step)
		}
		return steps, nil
	}
	steps, err := walk(root, resolver, "")
	if err != nil {
		return RelationPlan{}, err
	}
	plan := RelationPlan{Owner: owner, Steps: steps, BatchSize: 100, MaxRows: 10000}
	return plan, validateSteps(owner, steps, 0)
}

func cloneSteps(steps []RelationStep) []RelationStep {
	steps = slices.Clone(steps)
	for i := range steps {
		steps[i].Relation.FromColumns = slices.Clone(steps[i].Relation.FromColumns)
		steps[i].Relation.ToColumns = slices.Clone(steps[i].Relation.ToColumns)
		steps[i].Children = cloneSteps(steps[i].Children)
	}
	return steps
}

func validateSteps(owner RowDescriptor, steps []RelationStep, depth int) error {
	if owner.Table == nil || owner.bind == nil || owner.values == nil || owner.state == nil {
		return fmt.Errorf("relationship plan requires complete row descriptors")
	}
	if depth > 4 {
		return fmt.Errorf("relationship expansion exceeds four levels")
	}
	names := map[string]bool{}
	for _, step := range steps {
		r := step.Relation
		if step.Name == "" || names[step.Name] || r.From != owner.Table || r.To == nil || r.To != step.Target.Table || len(r.FromColumns) == 0 || len(r.FromColumns) > 65535 || len(r.FromColumns) != len(r.ToColumns) {
			return fmt.Errorf("invalid relationship step %q", step.Name)
		}
		names[step.Name] = true
		for i, name := range r.FromColumns {
			if _, ok := r.From.column(name); !ok {
				return fmt.Errorf("unknown relationship parent column %s", name)
			}
			column, ok := r.To.column(r.ToColumns[i])
			if !ok || column.DBType == "" {
				return fmt.Errorf("relationship target column requires SQL type metadata")
			}
		}
		if !step.Many && !r.To.unique(r.ToColumns) {
			return fmt.Errorf("relationship %s is not proven to-one", step.Name)
		}
		if err := validateSteps(step.Target, step.Children, depth+1); err != nil {
			return err
		}
	}
	return nil
}

type prefixedScanner struct {
	Scanner
	prefix any
}

func (s prefixedScanner) Scan(dest ...any) error {
	return s.Scanner.Scan(append([]any{s.prefix}, dest...)...)
}

func (s Session) loadRelations(ctx context.Context, owner RowDescriptor, parents []any, steps []RelationStep, batchSize, maxRows int, total *int) error {
	if len(steps) == 0 || len(parents) == 0 {
		return nil
	}
	if s.dialect != Postgres {
		return fmt.Errorf("automatic relationship plans require PostgreSQL")
	}
	for _, step := range steps {
		for _, parent := range parents {
			value := relatedValue{}
			if step.Many {
				value = relatedValue{value: []any{}, many: true}
			}
			owner.state(parent).set(step.Name, value)
		}
		children := []any{}
		// PostgreSQL binds at most 65535 parameters per statement.
		batchSize := min(batchSize, 65535/len(step.Relation.FromColumns))
		for start := 0; start < len(parents); start += batchSize {
			end := min(start+batchSize, len(parents))
			q, p, err := relationshipBatch(owner, step, parents[start:end])
			if err != nil {
				return err
			}
			if q == nil {
				continue
			}
			err = Each(ctx, s, q, p, func(value relatedRow) error {
				*total = *total + 1
				if *total > maxRows {
					return fmt.Errorf("relationship row bound exceeded")
				}
				if value.ordinal < 0 || value.ordinal >= end-start {
					return fmt.Errorf("relationship returned an invalid parent ordinal")
				}
				state := owner.state(parents[start+value.ordinal])
				loaded := state.values[step.Name]
				if step.Many {
					loaded.value = append(loaded.value.([]any), value.row)
				} else {
					if loaded.value != nil {
						return fmt.Errorf("to-one relationship returned multiple children")
					}
					loaded.value = value.row
				}
				state.set(step.Name, loaded)
				children = append(children, value.row)
				return nil
			})
			if err != nil {
				return err
			}
		}
		if err := s.loadRelations(ctx, step.Target, children, step.Children, batchSize, maxRows, total); err != nil {
			return err
		}
	}
	return nil
}

type relatedRow struct {
	ordinal int
	row     any
}

func relationshipBatch(owner RowDescriptor, step RelationStep, parents []any) (Buildable, Projection[relatedRow], error) {
	info := step.Target.Table.info
	label := "__bob_parent_ordinal"
	for i := 0; ; i++ {
		if _, exists := step.Target.Table.column(label); !exists {
			break
		}
		label = fmt.Sprintf("__bob_parent_ordinal_%d", i)
	}
	r := &renderer{dialect: Postgres}
	r.WriteString("SELECT k.ordinal AS ")
	r.ident(label)
	for _, column := range info.Columns {
		r.WriteString(", c.")
		r.ident(column.Name)
		r.WriteString(" AS ")
		r.ident(column.Name)
	}
	r.WriteString(" FROM (VALUES ")
	count := 0
	for ordinal, parent := range parents {
		values := owner.values(parent)
		if len(values) != len(owner.Table.info.Columns) {
			return nil, Projection[relatedRow]{}, fmt.Errorf("relationship values do not match column metadata")
		}
		key := make([]any, len(step.Relation.FromColumns))
		for i, name := range step.Relation.FromColumns {
			index := slices.IndexFunc(owner.Table.info.Columns, func(c ColumnInfo) bool { return c.Name == name })
			key[i] = values[index]
		}
		key, err := driverArgs(key)
		if err != nil {
			return nil, Projection[relatedRow]{}, err
		}
		if slices.Contains(key, nil) {
			continue
		}
		if count > 0 {
			r.WriteString(", ")
		}
		count++
		r.WriteByte('(')
		for i, value := range key {
			if i > 0 {
				r.WriteString(", ")
			}
			r.arg(value)
			r.WriteString("::")
			column, _ := step.Target.Table.column(step.Relation.ToColumns[i])
			// DBType comes from format_type in trusted catalog/schema metadata.
			r.WriteString(column.DBType)
		}
		fmt.Fprintf(&r.Builder, ", %d)", ordinal)
	}
	if count == 0 {
		return nil, Projection[relatedRow]{}, nil
	}
	r.WriteString(") AS k(")
	for i := range step.Relation.ToColumns {
		fmt.Fprintf(&r.Builder, "k%d, ", i)
	}
	r.WriteString("ordinal) JOIN ")
	writeTableName(r, step.Target.Table)
	r.WriteString(" AS c ON ")
	for i, name := range step.Relation.ToColumns {
		if i > 0 {
			r.WriteString(" AND ")
		}
		r.WriteString("c.")
		r.ident(name)
		fmt.Fprintf(&r.Builder, " = k.k%d", i)
	}
	r.WriteString(" ORDER BY k.ordinal")
	for _, name := range info.PrimaryKey {
		r.WriteString(", c.")
		r.ident(name)
	}
	p := step.Target.bind("c")
	columns := append([]Selection{Value(0).Select(label)}, p.Columns...)
	projection := Projection[relatedRow]{Columns: columns, Scan: func(scanner Scanner) (relatedRow, error) {
		var ordinal int
		row, err := p.Scan(prefixedScanner{scanner, &ordinal})
		return relatedRow{ordinal, row}, err
	}}
	return SQL(strings.TrimSpace(r.String()), r.args...), projection, nil
}
