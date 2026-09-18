package drivers

import (
	"reflect"
	"testing"
)

func TestTypeDependencyClosure(t *testing.T) {
	t.Parallel()

	types := Types{}
	types.Register("top", Type{DependsOn: []string{"middle"}})
	types.Register("middle", Type{DependsOn: []string{"leaf"}})
	types.Register("leaf", Type{DependsOn: []string{"top"}})

	if got, want := types.DependencyClosure("top"), []string{"top", "middle", "leaf"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dependency closure: want %v, got %v", want, got)
	}
}
