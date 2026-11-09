package set_test

import (
	"fmt"
	"maps"
	"slices"

	"github.com/ryan053857/go-kit/support/set"
)

func ExampleOf() {
	s := set.Of("go", "rust", "go")
	fmt.Println(s.Len(), s.Has("go"), s.Has("java"))
	// Output: 2 true false
}

func ExampleSet_Diff() {
	// Sync a user's roles: compare what is stored with what was requested.
	have := set.Of("admin", "editor")
	want := set.Of("editor", "viewer")
	fmt.Println(slices.Sorted(maps.Keys(want.Diff(have)))) // to add
	fmt.Println(slices.Sorted(maps.Keys(have.Diff(want)))) // to remove
	// Output:
	// [viewer]
	// [admin]
}

func ExampleSet_Intersect() {
	fmt.Println(slices.Sorted(maps.Keys(set.Of(1, 2, 3).Intersect(set.Of(2, 3, 4)))))
	// Output: [2 3]
}

func ExampleSet_Union() {
	fmt.Println(slices.Sorted(maps.Keys(set.Of(1, 2).Union(set.Of(2, 3)))))
	// Output: [1 2 3]
}
