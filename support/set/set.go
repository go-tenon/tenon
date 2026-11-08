// Package set 提供泛型集合 Set。
//
// Set 底层就是 map[T]struct{}，因此可以直接 len(s)、for v := range s，
// 也可以配合标准库使用：slices.Sorted(maps.Keys(s)) 得到有序切片，maps.Clone(s) 复制。
//
//	have := set.Of(existingIDs...)
//	want := set.Of(requestIDs...)
//	toAdd, toRemove := want.Diff(have), have.Diff(want)
package set

// Set 是元素类型为 T 的集合。零值为 nil：可以读（Has、Len、Slice），
// 但 Add 会 panic，请用 Of 或 make(Set[T]) 创建。
type Set[T comparable] map[T]struct{}

// Of 返回包含 vs 的新集合，重复值只保留一个。
func Of[T comparable](vs ...T) Set[T] {
	s := make(Set[T], len(vs))
	s.Add(vs...)
	return s
}

// Add 把 vs 加入集合。
func (s Set[T]) Add(vs ...T) {
	for _, v := range vs {
		s[v] = struct{}{}
	}
}

// Remove 从集合中删除 vs，不存在的值被忽略。
func (s Set[T]) Remove(vs ...T) {
	for _, v := range vs {
		delete(s, v)
	}
}

// Has 报告 v 是否在集合中。
func (s Set[T]) Has(v T) bool {
	_, ok := s[v]
	return ok
}

// Len 返回元素个数，等同于 len(s)。
func (s Set[T]) Len() int {
	return len(s)
}

// Slice 以非 nil 切片返回所有元素，顺序不确定；
// 需要有序结果时用 slices.Sorted(maps.Keys(s))。
func (s Set[T]) Slice() []T {
	out := make([]T, 0, len(s))
	for v := range s {
		out = append(out, v)
	}
	return out
}

// Union 返回 s 与 other 的并集，不修改两者。
func (s Set[T]) Union(other Set[T]) Set[T] {
	out := make(Set[T], max(len(s), len(other)))
	for v := range s {
		out[v] = struct{}{}
	}
	for v := range other {
		out[v] = struct{}{}
	}
	return out
}

// Intersect 返回同时在 s 与 other 中的元素，不修改两者。
func (s Set[T]) Intersect(other Set[T]) Set[T] {
	small, large := s, other
	if len(small) > len(large) {
		small, large = large, small
	}
	out := make(Set[T])
	for v := range small {
		if large.Has(v) {
			out[v] = struct{}{}
		}
	}
	return out
}

// Diff 返回在 s 中但不在 other 中的元素，不修改两者。
func (s Set[T]) Diff(other Set[T]) Set[T] {
	out := make(Set[T])
	for v := range s {
		if !other.Has(v) {
			out[v] = struct{}{}
		}
	}
	return out
}
