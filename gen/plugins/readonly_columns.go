package plugins

import (
	"github.com/stephenafamo/bob/gen"
	"github.com/stephenafamo/bob/gen/drivers"
)

// ReadOnlyColumnsConfig configures columns that should be selectable but omitted
// from generated mutation helpers.
//
// Columns applies to every generated table. Tables applies to a specific table
// key, such as "public.users" for Postgres schemas or "users" for unqualified
// table names.
type ReadOnlyColumnsConfig struct {
	Columns []string
	Tables  map[string][]string
}

// ReadOnlyColumns returns a plugin that marks the given columns as generated on
// every table before templates are executed. Bob's model templates include
// generated columns in selectable model/column definitions, but omit them from
// generated setters and insert/update expressions.
func ReadOnlyColumns[T, C, I any](columns ...string) gen.DBInfoPlugin[T, C, I] {
	return ReadOnlyColumnsWithConfig[T, C, I](ReadOnlyColumnsConfig{Columns: columns})
}

// ReadOnlyColumnsWithConfig returns a plugin that marks configured columns as
// generated before templates are executed.
func ReadOnlyColumnsWithConfig[T, C, I any](config ReadOnlyColumnsConfig) gen.DBInfoPlugin[T, C, I] {
	return readOnlyColumnsPlugin[T, C, I]{
		columns: stringSet(config.Columns),
		tables:  tableColumnsSet(config.Tables),
	}
}

type readOnlyColumnsPlugin[T, C, I any] struct {
	columns map[string]struct{}
	tables  map[string]map[string]struct{}
}

func (readOnlyColumnsPlugin[T, C, I]) Name() string {
	return "Read Only Columns Plugin"
}

func (p readOnlyColumnsPlugin[T, C, I]) PlugDBInfo(info *drivers.DBInfo[T, C, I]) error {
	for tableIndex := range info.Tables {
		table := &info.Tables[tableIndex]
		tableColumns := p.tables[table.Key]

		for columnIndex := range table.Columns {
			column := &table.Columns[columnIndex]
			if _, ok := p.columns[column.Name]; ok {
				column.Generated = true
				continue
			}
			if _, ok := tableColumns[column.Name]; ok {
				column.Generated = true
			}
		}
	}

	return nil
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func tableColumnsSet(values map[string][]string) map[string]map[string]struct{} {
	set := make(map[string]map[string]struct{}, len(values))
	for table, columns := range values {
		set[table] = stringSet(columns)
	}
	return set
}

var _ gen.DBInfoPlugin[any, any, any] = readOnlyColumnsPlugin[any, any, any]{}
