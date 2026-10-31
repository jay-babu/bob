// Package postgres reads PostgreSQL catalog facts directly into the versioned
// schema contract. It imports no legacy Bob generator or ORM implementation.
package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/stephenafamo/bob/next"
	"github.com/stephenafamo/bob/next/gen"
)

type Querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
type Mapping struct {
	GoType  string   `json:"go_type"`
	Imports []string `json:"imports,omitempty"`
}
type Options struct {
	Schemas           []string
	ImportPath        string
	IncludePartitions bool
	// Types is keyed by schema-qualified PostgreSQL type names, not legacy Go names.
	Types map[string]Mapping
}

type pgType struct {
	OID, Base, Element            uint32
	Schema, Name, Kind, Delimiter string
	Values                        []string
	NotNull                       bool
	Default                       string
}
type catalog struct {
	types     map[uint32]pgType
	resolved  map[uint32]Mapping
	visiting  map[uint32]bool
	generated map[string]gen.Type
	options   Options
}

// Inspect performs catalog reads only. Call it with a read-only repeatable-read
// transaction when a consistent schema snapshot is required.
func Inspect(ctx context.Context, db Querier, options Options) (gen.Schema, error) {
	s := gen.Schema{Version: gen.Version}
	if len(options.Schemas) == 0 || options.ImportPath == "" {
		return s, fmt.Errorf("schema allowlist and output import path are required")
	}
	c := catalog{types: map[uint32]pgType{}, resolved: map[uint32]Mapping{}, visiting: map[uint32]bool{}, generated: map[string]gen.Type{}, options: options}
	rows, err := db.QueryContext(ctx, `SELECT t.oid,n.nspname,t.typname,t.typtype::text,t.typbasetype,t.typelem,t.typdelim::text,t.typnotnull,COALESCE(t.typdefault,'') FROM pg_catalog.pg_type t JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace ORDER BY t.oid`)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var typ pgType
		if err := rows.Scan(&typ.OID, &typ.Schema, &typ.Name, &typ.Kind, &typ.Base, &typ.Element, &typ.Delimiter, &typ.NotNull, &typ.Default); err != nil {
			rows.Close()
			return s, err
		}
		c.types[typ.OID] = typ
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return s, err
	}
	if err := rows.Close(); err != nil {
		return s, err
	}
	rows, err = db.QueryContext(ctx, `SELECT enumtypid,enumlabel FROM pg_catalog.pg_enum ORDER BY enumtypid,enumsortorder`)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var oid uint32
		var value string
		if err := rows.Scan(&oid, &value); err != nil {
			rows.Close()
			return s, err
		}
		typ := c.types[oid]
		typ.Values = append(typ.Values, value)
		c.types[oid] = typ
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return s, err
	}
	if err := rows.Close(); err != nil {
		return s, err
	}
	encoded, err := json.Marshal(options.Schemas)
	if err != nil {
		return s, err
	}
	rows, err = db.QueryContext(ctx, `SELECT c.oid,n.nspname,c.relname,c.relkind::text,a.attname,a.atttypid,pg_catalog.format_type(a.atttypid,a.atttypmod),NOT a.attnotnull,COALESCE(pg_catalog.pg_get_expr(d.adbin,d.adrelid),''),a.attgenerated::text,a.attidentity::text
FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum
WHERE c.relkind IN ('r','p','v','m','f') AND n.nspname IN (SELECT jsonb_array_elements_text($1::jsonb))
AND (NOT c.relispartition OR $2)
AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dep WHERE dep.classid='pg_catalog.pg_class'::regclass AND dep.objid=c.oid AND dep.deptype='e')
ORDER BY n.nspname,c.relname,a.attnum`, string(encoded), options.IncludePartitions)
	if err != nil {
		return s, err
	}
	indices := map[uint32]int{}
	for rows.Next() {
		var oid, typeOID uint32
		var schema, name, kind, column, dbType, defaultExpr, generated, identity string
		var nullable bool
		if err := rows.Scan(&oid, &schema, &name, &kind, &column, &typeOID, &dbType, &nullable, &defaultExpr, &generated, &identity); err != nil {
			rows.Close()
			return s, err
		}
		index, exists := indices[oid]
		if !exists {
			index = len(s.Tables)
			indices[oid] = index
			s.Tables = append(s.Tables, gen.Table{Key: schema + "." + name, Package: packageName(schema + "_" + name), Info: next.TableInfo{Schema: schema, Name: name, ReadOnly: kind == "v" || kind == "m" || kind == "f"}})
		}
		mapping, err := c.resolve(typeOID)
		if err != nil {
			rows.Close()
			return s, fmt.Errorf("column %s.%s.%s: %w", schema, name, column, err)
		}
		table := &s.Tables[index]
		domainNotNull, domainDefault := false, false
		for typ := c.types[typeOID]; typ.Kind == "d"; typ = c.types[typ.Base] {
			domainNotNull = domainNotNull || typ.NotNull
			domainDefault = domainDefault || typ.Default != ""
		}
		effectiveNullable := nullable && !domainNotNull
		if table.Info.ReadOnly {
			effectiveNullable = nullable
		}
		table.Info.Columns = append(table.Info.Columns, next.ColumnInfo{Name: column, DBType: dbType, Nullable: effectiveNullable, HasDefault: defaultExpr != "" || identity != "" || domainDefault, Generated: generated != "" || identity == "a"})
		goName := gen.Identifier(column)
		switch goName {
		case "Ref", "Projection", "Assignments":
			goName += "Value"
		}
		if goName == "" || slices.ContainsFunc(table.Columns, func(c gen.Column) bool { return c.GoName == goName }) {
			hash := sha256.Sum256([]byte(column))
			goName = "Field" + goName + fmt.Sprintf("%x", hash[:4])
		}
		table.Columns = append(table.Columns, gen.Column{Name: column, GoName: goName, GoType: mapping.GoType})
		table.Imports = append(table.Imports, mapping.Imports...)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return s, err
	}
	if err := rows.Close(); err != nil {
		return s, err
	}
	// JSON preserves ordered key columns without coupling catalog reads to a driver's array decoder.
	rows, err = db.QueryContext(ctx, `SELECT con.conrelid,con.contype::text,con.conname,con.confrelid,
COALESCE((SELECT jsonb_agg(a.attname ORDER BY k.ord) FROM unnest(con.conkey) WITH ORDINALITY k(attnum,ord) JOIN pg_catalog.pg_attribute a ON a.attrelid=con.conrelid AND a.attnum=k.attnum),'[]'::jsonb)::text,
COALESCE((SELECT jsonb_agg(a.attname ORDER BY k.ord) FROM unnest(con.confkey) WITH ORDINALITY k(attnum,ord) JOIN pg_catalog.pg_attribute a ON a.attrelid=con.confrelid AND a.attnum=k.attnum),'[]'::jsonb)::text
FROM pg_catalog.pg_constraint con JOIN pg_catalog.pg_class cl ON cl.oid=con.conrelid JOIN pg_catalog.pg_namespace n ON n.oid=cl.relnamespace
WHERE con.contype IN ('p','u','f') AND n.nspname IN (SELECT jsonb_array_elements_text($1::jsonb)) ORDER BY con.conrelid,con.conname`, string(encoded))
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var oid, target uint32
		var kind, name, localJSON, foreignJSON string
		if err := rows.Scan(&oid, &kind, &name, &target, &localJSON, &foreignJSON); err != nil {
			rows.Close()
			return s, err
		}
		index, ok := indices[oid]
		if !ok {
			continue
		}
		var local, foreign []string
		if err := json.Unmarshal([]byte(localJSON), &local); err != nil {
			rows.Close()
			return s, err
		}
		if err := json.Unmarshal([]byte(foreignJSON), &foreign); err != nil {
			rows.Close()
			return s, err
		}
		table := &s.Tables[index]
		switch kind {
		case "p":
			table.Info.PrimaryKey = local
		case "u":
			table.Info.UniqueKeys = append(table.Info.UniqueKeys, local)
		case "f":
			if ti, ok := indices[target]; ok {
				s.Relations = append(s.Relations, gen.Relation{Name: packageName(table.Key + "_" + name), From: table.Key, To: s.Tables[ti].Key, FromColumns: local, ToColumns: foreign})
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return s, err
	}
	if err := rows.Close(); err != nil {
		return s, err
	}
	rows, err = db.QueryContext(ctx, `SELECT ix.indrelid,jsonb_agg(a.attname ORDER BY k.ord)::text FROM pg_catalog.pg_index ix
JOIN pg_catalog.pg_class cl ON cl.oid=ix.indrelid JOIN pg_catalog.pg_namespace n ON n.oid=cl.relnamespace
CROSS JOIN LATERAL unnest(ix.indkey::smallint[]) WITH ORDINALITY k(attnum,ord)
JOIN pg_catalog.pg_attribute a ON a.attrelid=ix.indrelid AND a.attnum=k.attnum
WHERE ix.indisunique AND ix.indisvalid AND ix.indisready AND ix.indpred IS NULL AND ix.indexprs IS NULL AND k.ord<=ix.indnkeyatts
AND n.nspname IN (SELECT jsonb_array_elements_text($1::jsonb)) GROUP BY ix.indrelid,ix.indexrelid ORDER BY ix.indrelid,ix.indexrelid`, string(encoded))
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var oid uint32
		var encoded string
		if err := rows.Scan(&oid, &encoded); err != nil {
			rows.Close()
			return s, err
		}
		index, ok := indices[oid]
		if !ok {
			continue
		}
		var columns []string
		if err := json.Unmarshal([]byte(encoded), &columns); err != nil {
			rows.Close()
			return s, err
		}
		info := &s.Tables[index].Info
		if slices.Equal(info.PrimaryKey, columns) || slices.ContainsFunc(info.UniqueKeys, func(key []string) bool { return slices.Equal(key, columns) }) {
			continue
		}
		info.UniqueKeys = append(info.UniqueKeys, columns)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return s, err
	}
	if err := rows.Close(); err != nil {
		return s, err
	}
	for _, typ := range c.generated {
		s.Types = append(s.Types, typ)
	}
	slices.SortFunc(s.Types, func(a, b gen.Type) int { return strings.Compare(a.Package, b.Package) })
	for i := range s.Tables {
		slices.Sort(s.Tables[i].Imports)
		s.Tables[i].Imports = slices.Compact(s.Tables[i].Imports)
	}
	return s, s.Validate()
}

func (c *catalog) resolve(oid uint32) (Mapping, error) {
	if result, ok := c.resolved[oid]; ok {
		return result, nil
	}
	typ, ok := c.types[oid]
	if !ok {
		return Mapping{}, fmt.Errorf("unknown type OID")
	}
	key := typ.Schema + "." + typ.Name
	if override, ok := c.options.Types[key]; ok {
		if override.GoType == "" {
			return Mapping{}, fmt.Errorf("empty mapping for %s", key)
		}
		c.resolved[oid] = override
		return override, nil
	}
	if c.visiting[oid] {
		return Mapping{}, fmt.Errorf("recursive SQL type %s needs an explicit mapping", key)
	}
	c.visiting[oid] = true
	defer delete(c.visiting, oid)
	var result Mapping
	if typ.Kind == "e" || typ.Kind == "d" {
		pkg := packageName(typ.Schema + "_" + typ.Name)
		definition := gen.Type{Package: pkg, Kind: "enum", Values: typ.Values}
		if typ.Kind == "d" {
			base, err := c.resolve(typ.Base)
			if err != nil {
				return result, err
			}
			definition = gen.Type{Package: pkg, Kind: "domain", Base: base.GoType, Imports: base.Imports}
		}
		c.generated[pkg] = definition
		result = Mapping{GoType: pkg + ".Value", Imports: []string{pkg + " " + strconv.Quote(c.options.ImportPath+"/types/"+pkg)}}
	} else if typ.Element != 0 && c.types[typ.Element].Delimiter == "," {
		element, err := c.resolve(typ.Element)
		if err != nil {
			return result, err
		}
		result = Mapping{GoType: "next.Array[" + element.GoType + "]", Imports: element.Imports}
	} else if typ.Schema == "pg_catalog" {
		switch typ.Name {
		case "bool":
			result.GoType = "bool"
		case "int2":
			result.GoType = "int16"
		case "int4":
			result.GoType = "int32"
		case "int8":
			result.GoType = "int64"
		case "float4":
			result.GoType = "float32"
		case "float8":
			result.GoType = "float64"
		case "numeric":
			result.GoType = "next.Decimal"
		case "bytea":
			result.GoType = "[]byte"
		case "date", "timestamp", "timestamptz":
			result = Mapping{GoType: "time.Time", Imports: []string{`"time"`}}
		case "json", "jsonb":
			result = Mapping{GoType: "json.RawMessage", Imports: []string{`"encoding/json"`}}
		case "oid", "xid", "cid":
			result.GoType = "uint32"
		case "text", "varchar", "bpchar", "name", "char", "uuid", "inet", "cidr", "macaddr", "macaddr8", "time", "timetz", "interval", "bit", "varbit", "xml", "pg_lsn", "tsvector", "tsquery", "regclass", "regtype", "regproc", "regprocedure", "regnamespace", "regrole":
			result.GoType = "string"
		}
	}
	if result.GoType == "" {
		// Unknown SQL types retain a distinct, lossless text representation. No
		// dependency on extension SDKs is imposed on every schema consumer.
		pkg := packageName(typ.Schema + "_" + typ.Name)
		c.generated[pkg] = gen.Type{Package: pkg, Kind: "text"}
		result = Mapping{GoType: pkg + ".Value", Imports: []string{pkg + " " + strconv.Quote(c.options.ImportPath+"/types/"+pkg)}}
	}
	c.resolved[oid] = result
	return result, nil
}

func packageName(key string) string {
	var b strings.Builder
	changed := false
	for _, r := range key {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
			changed = changed || unicode.ToLower(r) != r
		} else {
			b.WriteByte('_')
			changed = true
		}
	}
	name := b.String()
	if name == "" || unicode.IsDigit([]rune(name)[0]) || token.Lookup(name).IsKeyword() {
		name = "db_" + name
	}
	if changed {
		sum := sha256.Sum256([]byte(key))
		name += fmt.Sprintf("_%x", sum[:4])
	}
	return name
}
