package gen

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stephenafamo/bob/gen/drivers"
)

const (
	modelPackageSplitModeRelationshipComponents = "relationship_components"
	defaultModelPackageSplitInternalDir         = "internal/components"

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
	ID          string
	Package     string
	OutFolder   string
	PackagePath string
	TableKeys   []string
}

func buildModelSplitData[C, I any](
	config ModelPackageSplit,
	rootOutFolder string,
	rootPackagePath string,
	tables drivers.Tables[C, I],
	relationships Relationships,
) (*ModelSplitData, error) {
	if config.Mode == "" {
		return nil, nil
	}

	if config.Mode != modelPackageSplitModeRelationshipComponents {
		return nil, fmt.Errorf("unknown model package split mode %q", config.Mode)
	}

	internalDir := config.InternalDir
	if internalDir == "" {
		internalDir = defaultModelPackageSplitInternalDir
	}

	graph := make(map[string]map[string]struct{}, len(tables))
	tableSet := make(map[string]struct{}, len(tables))
	for _, table := range tables {
		graph[table.Key] = map[string]struct{}{}
		tableSet[table.Key] = struct{}{}
	}

	addEdge := func(from, to string) {
		if from == to {
			return
		}
		if _, ok := tableSet[from]; !ok {
			return
		}
		if _, ok := tableSet[to]; !ok {
			return
		}
		graph[from][to] = struct{}{}
	}

	for tableKey, rels := range relationships {
		for _, rel := range rels {
			addEdge(tableKey, rel.Foreign())
			for _, side := range rel.Sides {
				addEdge(tableKey, side.From)
				addEdge(tableKey, side.To)
			}
		}
	}

	components := stronglyConnectedModelComponents(graph)
	slices.SortFunc(components, func(a, b []string) int {
		return strings.Compare(strings.Join(a, "\x00"), strings.Join(b, "\x00"))
	})

	data := &ModelSplitData{
		Enabled:         true,
		Mode:            config.Mode,
		InternalDir:     internalDir,
		RootOutFolder:   rootOutFolder,
		RootPackagePath: rootPackagePath,
		Components:      make([]*ModelSplitComponent, 0, len(components)),
		TableComponents: make(map[string]*ModelSplitComponent, len(tables)),
	}

	for _, componentTables := range components {
		id := stableModelComponentID(componentTables)
		pkg := "c" + id
		component := &ModelSplitComponent{
			ID:          id,
			Package:     pkg,
			OutFolder:   filepath.Join(rootOutFolder, filepath.FromSlash(internalDir), pkg),
			PackagePath: path.Join(rootPackagePath, filepath.ToSlash(internalDir), pkg),
			TableKeys:   componentTables,
		}
		data.Components = append(data.Components, component)
		for _, tableKey := range componentTables {
			data.TableComponents[tableKey] = component
		}
	}

	return data, nil
}

func stronglyConnectedModelComponents(graph map[string]map[string]struct{}) [][]string {
	var (
		index      int
		stack      []string
		onStack    = map[string]struct{}{}
		indices    = map[string]int{}
		lowLinks   = map[string]int{}
		components [][]string
	)

	var visit func(string)
	visit = func(v string) {
		indices[v] = index
		lowLinks[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = struct{}{}

		neighbors := make([]string, 0, len(graph[v]))
		for w := range graph[v] {
			neighbors = append(neighbors, w)
		}
		slices.Sort(neighbors)

		for _, w := range neighbors {
			if _, ok := indices[w]; !ok {
				visit(w)
				lowLinks[v] = min(lowLinks[v], lowLinks[w])
				continue
			}
			if _, ok := onStack[w]; ok {
				lowLinks[v] = min(lowLinks[v], indices[w])
			}
		}

		if lowLinks[v] != indices[v] {
			return
		}

		var component []string
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			delete(onStack, w)
			component = append(component, w)
			if w == v {
				break
			}
		}
		slices.Sort(component)
		components = append(components, component)
	}

	nodes := make([]string, 0, len(graph))
	for node := range graph {
		nodes = append(nodes, node)
	}
	slices.Sort(nodes)
	for _, node := range nodes {
		if _, ok := indices[node]; !ok {
			visit(node)
		}
	}

	return components
}

func stableModelComponentID(tableKeys []string) string {
	hash := sha1.Sum([]byte(strings.Join(tableKeys, "\x00")))
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
