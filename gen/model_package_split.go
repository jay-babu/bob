package gen

import (
	"crypto/sha1" //nolint:gosec // used only for stable non-security identifiers
	"encoding/hex"
	"go/token"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/stephenafamo/bob/gen/drivers"
	"github.com/stephenafamo/bob/orm"
)

const (
	modelPackageSplitModeTablePackages = "table_packages"
	factoryRelationshipsPackage        = "relationships"
	factoryParentsPackage              = "parents"

	modelSplitGenerationFacade    = "facade"
	modelSplitGenerationComponent = "component"
)

type ModelSplitData struct {
	Enabled          bool
	Mode             string
	InternalDir      string
	RootOutFolder    string
	RootPackagePath  string
	Generation       string
	CurrentComponent *ModelSplitComponent
	Components       []*ModelSplitComponent
	TableComponents  map[string]*ModelSplitComponent
}

type ModelSplitComponent struct {
	ID           string
	Package      string
	ImportAlias  string
	RelativePath string
	OutFolder    string
	PackagePath  string
	TableKeys    []string
}

func (d *ModelSplitData) GeneratesFacade() bool {
	return false
}

func buildModelSplitData[C, I any](
	rootOutFolder string,
	rootPackagePath string,
	tables drivers.Tables[C, I],
) *ModelSplitData {
	data := &ModelSplitData{
		Enabled:         true,
		Mode:            modelPackageSplitModeTablePackages,
		RootOutFolder:   rootOutFolder,
		RootPackagePath: rootPackagePath,
		Components:      make([]*ModelSplitComponent, 0, len(tables)),
		TableComponents: make(map[string]*ModelSplitComponent, len(tables)),
	}

	sortedTables := slices.Clone(tables)
	slices.SortFunc(sortedTables, func(a, b drivers.Table[C, I]) int {
		return strings.Compare(a.Key, b.Key)
	})
	for _, table := range sortedTables {
		schema, _, found := strings.Cut(table.Key, ".")
		if !found || schema == "" {
			schema = "public"
		}
		packageSchema := safeModelPackageSegment(schema)
		packageTablePath := safeModelPackageSegment(table.Name)
		packageName := safeGoPackageName(table.Name)
		relativePath := path.Join(packageSchema, packageTablePath)
		component := &ModelSplitComponent{
			ID:           table.Key,
			Package:      packageName,
			ImportAlias:  packageName,
			RelativePath: relativePath,
			OutFolder:    filepath.Join(rootOutFolder, filepath.FromSlash(relativePath)),
			PackagePath:  path.Join(rootPackagePath, relativePath),
			TableKeys:    []string{table.Key},
		}
		data.Components = append(data.Components, component)
		data.TableComponents[table.Key] = component
	}

	aliasCounts := make(map[string]int, len(data.Components))
	for _, component := range data.Components {
		aliasCounts[component.ImportAlias]++
	}
	for _, component := range data.Components {
		if aliasCounts[component.ImportAlias] > 1 {
			schema, _, _ := strings.Cut(component.RelativePath, "/")
			component.ImportAlias = safeGoPackageName(schema) + component.Package
		}
	}

	clear(aliasCounts)
	for _, component := range data.Components {
		aliasCounts[component.ImportAlias]++
	}
	for _, component := range data.Components {
		if aliasCounts[component.ImportAlias] > 1 {
			component.ImportAlias += stableModelComponentID(component.TableKeys)[:8]
		}
	}

	return data
}

func safeGoPackageName(raw string) string {
	normalized := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r):
			return unicode.ToLower(r)
		case unicode.IsDigit(r):
			return r
		default:
			return -1
		}
	}, raw)
	if normalized == "" {
		normalized = "model"
	}
	if !token.IsIdentifier(normalized) {
		normalized = "model" + normalized
	}
	return normalized
}

func safeModelPackageSegment(raw string) string {
	normalized := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r):
			return unicode.ToLower(r)
		case unicode.IsDigit(r), r == '_':
			return r
		default:
			return '_'
		}
	}, raw)
	if normalized == "" {
		normalized = "model"
	}
	if !token.IsIdentifier(normalized) {
		normalized = "model_" + normalized
	}
	if normalized != raw || !token.IsIdentifier(raw) {
		normalized += "_" + stableModelComponentID([]string{raw})[:8]
	}
	return normalized
}

func prepareTablePackageRelationships(relationships Relationships) Relationships {
	forward := make(Relationships, len(relationships))
	for table, rels := range relationships {
		for _, rel := range rels {
			if len(rel.Sides) == 1 && rel.Sides[0].Modify == "from" {
				forward[table] = append(forward[table], rel)
			}
		}
	}

	return breakRelationshipCycles(forward)
}

func prepareFactoryParentRelationships(relationships Relationships) Relationships {
	parents := make(Relationships, len(relationships))
	for table, rels := range relationships {
		for _, rel := range rels {
			if len(rel.Sides) == 1 && rel.Sides[0].Modify == "from" && !rel.IsToMany() {
				parents[table] = append(parents[table], rel)
			}
		}
	}

	return breakRelationshipCycles(parents)
}

func buildFactoryParentsSplit(
	modelSplit *ModelSplitData,
	rootTableKey string,
	rootOutFolder string,
	rootPackagePath string,
	relationships Relationships,
) (*ModelSplitData, Relationships) {
	selected := map[string]struct{}{}
	queue := []string{rootTableKey}
	for len(queue) > 0 {
		tableKey := queue[0]
		queue = queue[1:]
		if _, ok := selected[tableKey]; ok {
			continue
		}
		selected[tableKey] = struct{}{}
		for _, rel := range relationships.Get(tableKey) {
			queue = append(queue, rel.Foreign())
		}
	}

	rootComponent := modelSplit.TableComponents[rootTableKey]
	relativePath := path.Join(rootComponent.RelativePath, factoryParentsPackage)
	component := &ModelSplitComponent{
		ID:           rootTableKey + "/" + factoryParentsPackage,
		Package:      factoryParentsPackage,
		ImportAlias:  factoryParentsPackage,
		RelativePath: relativePath,
		OutFolder:    filepath.Join(rootOutFolder, filepath.FromSlash(relativePath)),
		PackagePath:  path.Join(rootPackagePath, relativePath),
	}
	parentSplit := &ModelSplitData{
		Enabled:         true,
		Mode:            modelSplit.Mode,
		RootOutFolder:   rootOutFolder,
		RootPackagePath: rootPackagePath,
		Components:      []*ModelSplitComponent{component},
		TableComponents: make(map[string]*ModelSplitComponent, len(selected)),
	}
	for _, originalComponent := range modelSplit.Components {
		for _, tableKey := range originalComponent.TableKeys {
			if _, ok := selected[tableKey]; !ok {
				continue
			}
			component.TableKeys = append(component.TableKeys, tableKey)
			parentSplit.TableComponents[tableKey] = component
		}
	}

	closureRelationships := make(Relationships, len(selected))
	for _, tableKey := range component.TableKeys {
		for _, rel := range relationships.Get(tableKey) {
			if _, ok := selected[rel.Foreign()]; ok {
				closureRelationships[tableKey] = append(closureRelationships[tableKey], rel)
			}
		}
	}

	return parentSplit, closureRelationships
}

func breakRelationshipCycles(relationships Relationships) Relationships {
	type edge struct {
		from string
		to   string
		rel  orm.Relationship
	}

	edges := make([]edge, 0)
	for table, rels := range relationships {
		for _, rel := range rels {
			edges = append(edges, edge{from: table, to: rel.Foreign(), rel: rel})
		}
	}
	slices.SortFunc(edges, func(a, b edge) int {
		tableName := func(key string) string {
			if index := strings.LastIndexByte(key, '.'); index >= 0 {
				return key[index+1:]
			}
			return key
		}
		if c := strings.Compare(tableName(a.from), tableName(b.from)); c != 0 {
			return c
		}
		if c := strings.Compare(a.from, b.from); c != 0 {
			return c
		}
		if c := strings.Compare(a.to, b.to); c != 0 {
			return c
		}
		return strings.Compare(a.rel.Name, b.rel.Name)
	})

	kept := make(Relationships, len(relationships))
	graph := make(map[string]map[string]struct{}, len(relationships))
	var reaches func(string, string, map[string]struct{}) bool
	reaches = func(from, target string, seen map[string]struct{}) bool {
		if from == target {
			return true
		}
		if _, ok := seen[from]; ok {
			return false
		}
		seen[from] = struct{}{}
		for next := range graph[from] {
			if reaches(next, target, seen) {
				return true
			}
		}
		return false
	}

	for _, e := range edges {
		if e.from != e.to && reaches(e.to, e.from, map[string]struct{}{}) {
			continue
		}
		kept[e.from] = append(kept[e.from], e.rel)
		if graph[e.from] == nil {
			graph[e.from] = map[string]struct{}{}
		}
		graph[e.from][e.to] = struct{}{}
	}

	return kept
}

func stableModelComponentID(tableKeys []string) string {
	hash := sha1.Sum([]byte(strings.Join(tableKeys, "\x00"))) //nolint:gosec // this is a stable identifier, not a security digest
	return hex.EncodeToString(hash[:])[:10]
}

func filterTablesForComponent[C, I any](tables drivers.Tables[C, I], component *ModelSplitComponent) drivers.Tables[C, I] {
	keys := make(map[string]struct{}, len(component.TableKeys))
	for _, key := range component.TableKeys {
		keys[key] = struct{}{}
	}

	filtered := make(drivers.Tables[C, I], 0, len(component.TableKeys))
	for _, table := range tables {
		if _, ok := keys[table.Key]; ok {
			filtered = append(filtered, table)
		}
	}
	return filtered
}

func modelSplitForOutput(split *ModelSplitData, rootOutFolder, rootPackagePath string) *ModelSplitData {
	if split == nil || !split.Enabled {
		return split
	}

	clone := &ModelSplitData{
		Enabled:         split.Enabled,
		Mode:            split.Mode,
		InternalDir:     split.InternalDir,
		RootOutFolder:   rootOutFolder,
		RootPackagePath: rootPackagePath,
		Generation:      split.Generation,
		Components:      make([]*ModelSplitComponent, 0, len(split.Components)),
		TableComponents: make(map[string]*ModelSplitComponent, len(split.TableComponents)),
	}

	for _, component := range split.Components {
		c := &ModelSplitComponent{
			ID:           component.ID,
			Package:      component.Package,
			ImportAlias:  component.ImportAlias,
			RelativePath: component.RelativePath,
			OutFolder:    filepath.Join(rootOutFolder, filepath.FromSlash(component.RelativePath)),
			PackagePath:  path.Join(rootPackagePath, component.RelativePath),
			TableKeys:    slices.Clone(component.TableKeys),
		}
		clone.Components = append(clone.Components, c)
		if split.CurrentComponent != nil && split.CurrentComponent.ID == component.ID {
			clone.CurrentComponent = c
		}
		for _, tableKey := range c.TableKeys {
			clone.TableComponents[tableKey] = c
		}
	}

	return clone
}

func modelSplitForNestedOutput(split *ModelSplitData, rootOutFolder, rootPackagePath, nestedPackage string) *ModelSplitData {
	clone := modelSplitForOutput(split, rootOutFolder, rootPackagePath)
	if clone == nil || !clone.Enabled {
		return clone
	}

	aliasCounts := make(map[string]int, len(clone.Components))
	for _, component := range clone.Components {
		component.Package = safeGoPackageName(nestedPackage)
		component.ImportAlias = safeGoPackageName(component.ImportAlias + nestedPackage)
		aliasCounts[component.ImportAlias]++
		component.RelativePath = path.Join(component.RelativePath, nestedPackage)
		component.OutFolder = filepath.Join(rootOutFolder, filepath.FromSlash(component.RelativePath))
		component.PackagePath = path.Join(rootPackagePath, component.RelativePath)
	}
	for _, component := range clone.Components {
		if aliasCounts[component.ImportAlias] > 1 {
			component.ImportAlias += stableModelComponentID(component.TableKeys)[:8]
		}
	}

	return clone
}
