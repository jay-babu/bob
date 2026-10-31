package next

import (
	"context"
	"fmt"
)

// Loaded separates a loaded-but-absent relationship from one not requested.
type Loaded[T any] struct {
	Value    T
	IsLoaded bool
}
type WithMany[P, C any] struct {
	Parent  P
	Related Loaded[[]C]
}

// BatchLoader never mutates parent rows. K can be a comparable struct for a
// composite key. The caller supplies the query, including tenant predicates and
// deterministic ordering. A false key flag represents SQL NULL and is not loaded.
type BatchLoader[P, C any, K comparable] struct {
	ParentKey  func(P) (K, bool)
	ChildKey   func(C) K
	Query      func([]K) Buildable
	Projection Projection[C]
	BatchSize  int
	MaxRows    int
}

func LoadMany[P, C any, K comparable](ctx context.Context, s Session, parents []P, loader BatchLoader[P, C, K]) ([]WithMany[P, C], error) {
	if loader.ParentKey == nil || loader.ChildKey == nil || loader.Query == nil || loader.BatchSize <= 0 || loader.MaxRows <= 0 {
		return nil, fmt.Errorf("loader requires key functions, query, positive batch size and total row bound")
	}
	keys := make([]K, 0, len(parents))
	seen := map[K]bool{}
	for _, p := range parents {
		k, ok := loader.ParentKey(p)
		if ok && !seen[k] {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	grouped := map[K][]C{}
	total := 0
	for start := 0; start < len(keys); start += loader.BatchSize {
		end := min(start+loader.BatchSize, len(keys))
		batch := append([]K(nil), keys[start:end]...)
		batchKeys := map[K]bool{}
		for _, k := range batch {
			batchKeys[k] = true
		}
		err := Each(ctx, s, loader.Query(batch), loader.Projection, func(child C) error {
			total++
			if total > loader.MaxRows {
				return fmt.Errorf("relationship row bound exceeded")
			}
			k := loader.ChildKey(child)
			if !batchKeys[k] {
				return fmt.Errorf("relationship query returned a key outside its batch")
			}
			grouped[k] = append(grouped[k], child)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	result := make([]WithMany[P, C], len(parents))
	for i, p := range parents {
		var children []C
		if k, ok := loader.ParentKey(p); ok {
			children = append([]C{}, grouped[k]...)
		} else {
			children = []C{}
		}
		result[i] = WithMany[P, C]{p, Loaded[[]C]{children, true}}
	}
	return result, nil
}

type WithOne[P, C any] struct {
	Parent  P
	Related Loaded[*C]
}

// LoadOne enforces to-one cardinality rather than choosing an arbitrary child.
func LoadOne[P, C any, K comparable](ctx context.Context, s Session, parents []P, loader BatchLoader[P, C, K]) ([]WithOne[P, C], error) {
	many, err := LoadMany(ctx, s, parents, loader)
	if err != nil {
		return nil, err
	}
	result := make([]WithOne[P, C], len(many))
	for i, item := range many {
		if len(item.Related.Value) > 1 {
			return nil, fmt.Errorf("to-one relationship returned multiple children")
		}
		var child *C
		if len(item.Related.Value) == 1 {
			child = &item.Related.Value[0]
		}
		result[i] = WithOne[P, C]{item.Parent, Loaded[*C]{child, true}}
	}
	return result, nil
}
