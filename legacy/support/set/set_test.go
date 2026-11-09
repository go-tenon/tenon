package set

import (
	"maps"
	"slices"
	"testing"
)

func sorted(s Set[int]) []int {
	return slices.Sorted(maps.Keys(s))
}

func TestOfAddRemoveHas(t *testing.T) {
	s := Of(1, 2, 2, 3)
	if s.Len() != 3 {
		t.Fatalf("Len = %d, want 3", s.Len())
	}
	s.Add(4, 1)
	s.Remove(2, 99)
	if got := sorted(s); !slices.Equal(got, []int{1, 3, 4}) {
		t.Errorf("elements = %v, want [1 3 4]", got)
	}
	if !s.Has(3) || s.Has(2) {
		t.Errorf("Has(3) = %v, Has(2) = %v; want true, false", s.Has(3), s.Has(2))
	}
}

func TestNilSetReads(t *testing.T) {
	var s Set[int]
	if s.Has(1) || s.Len() != 0 || s.Slice() == nil || len(s.Slice()) != 0 {
		t.Errorf("nil set: Has=%v Len=%d Slice=%#v", s.Has(1), s.Len(), s.Slice())
	}
	s.Remove(1) // must not panic
	if got := s.Union(Of(1)); !got.Has(1) {
		t.Errorf("nil.Union({1}) = %v", got)
	}
}

func TestSlice(t *testing.T) {
	got := Of(3, 1, 2).Slice()
	slices.Sort(got)
	if !slices.Equal(got, []int{1, 2, 3}) {
		t.Errorf("Slice = %v, want [1 2 3]", got)
	}
}

func TestSetOperations(t *testing.T) {
	cases := []struct {
		name                     string
		a, b                     Set[int]
		union, intersect, aMinus []int
	}{
		{"overlap", Of(1, 2, 3), Of(2, 3, 4), []int{1, 2, 3, 4}, []int{2, 3}, []int{1}},
		{"disjoint", Of(1), Of(2), []int{1, 2}, []int{}, []int{1}},
		{"empty other", Of(1, 2), Of[int](), []int{1, 2}, []int{}, []int{1, 2}},
		{"nil both", nil, nil, []int{}, []int{}, []int{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			aBefore, bBefore := maps.Clone(c.a), maps.Clone(c.b)
			if got := sorted(c.a.Union(c.b)); !slices.Equal(got, c.union) {
				t.Errorf("Union = %v, want %v", got, c.union)
			}
			if got := sorted(c.a.Intersect(c.b)); !slices.Equal(got, c.intersect) {
				t.Errorf("Intersect = %v, want %v", got, c.intersect)
			}
			if got := sorted(c.a.Diff(c.b)); !slices.Equal(got, c.aMinus) {
				t.Errorf("Diff = %v, want %v", got, c.aMinus)
			}
			if !maps.Equal(c.a, aBefore) || !maps.Equal(c.b, bBefore) {
				t.Errorf("operands mutated: a=%v b=%v", c.a, c.b)
			}
		})
	}
}
