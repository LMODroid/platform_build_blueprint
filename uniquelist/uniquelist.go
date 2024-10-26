// Copyright 2024 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package uniquelist

import (
	"iter"
	"slices"
	"unique"
)

// UniqueList is a workaround for Go limitation that slices are not comparable and
// thus can't be used with unique.Make.  It interns slices by storing them in an
// unrolled linked list, where each node has a fixed size array, which are comparable
// and can be stored using the unique package.  A UniqueList is immutable.
type UniqueList[T comparable] struct {
	handle unique.Handle[node[T]]
}

// Len returns the length of the slice that was originally passed to Make.  It returns
// a stored value and does not require iterating the linked list.
func (s UniqueList[T]) Len() int {
	var zeroList unique.Handle[node[T]]
	if s.handle == zeroList {
		return 0
	}

	return s.handle.Value().len
}

// ToSlice returns a slice containing a shallow copy of the list.
func (s UniqueList[T]) ToSlice() []T {
	return s.AppendTo(nil)
}

// Iter returns a iter.Seq that iterates the elements of the list.
func (s UniqueList[T]) Iter() iter.Seq[T] {
	var zeroSlice unique.Handle[node[T]]

	return func(yield func(T) bool) {
		cur := s.handle
		for cur != zeroSlice {
			impl := cur.Value()
			for _, v := range impl.elements[:min(nodeSize, impl.len)] {
				if !yield(v) {
					return
				}
			}
			cur = impl.next
		}
	}
}

// iterNodes returns an iter.Seq that iterates each node of the
// unrolled linked list, returning a slice that contains all the
// elements in a node at once.
func (s UniqueList[T]) iterNodes() iter.Seq[[]T] {
	var zeroSlice unique.Handle[node[T]]

	return func(yield func([]T) bool) {
		cur := s.handle
		for cur != zeroSlice {
			impl := cur.Value()
			l := min(impl.len, len(impl.elements))
			if !yield(impl.elements[:l]) {
				return
			}
			cur = impl.next
		}
	}
}

// AppendTo appends the contents of the list to the given slice and returns
// the results.
func (s UniqueList[T]) AppendTo(slice []T) []T {
	// TODO: should this grow by more than s.Len() to amortize reallocation costs?
	slices.Grow(slice, s.Len())
	for chunk := range s.iterNodes() {
		slice = append(slice, chunk...)
	}
	return slice
}

// node is a node in an unrolled linked list object that holds a group of elements of a
// list in a fixed size array in order to satisfy the comparable constraint.
type node[T comparable] struct {
	// elements is a group of up to nodeSize elements of a list.
	elements [nodeSize]T

	// len is the length of the list stored in this node and any transitive linked nodes.
	// If len is less than nodeSize then only the first len values in the elements array
	// are part of the list.  If len is greater than nodeSize then next will point to the
	// next node in the unrolled linked list.
	len int

	// next is the next node in the linked list.  If it is the zero value of unique.Handle
	// then this is the last node.
	next unique.Handle[node[T]]
}

// nodeSize is the number of list elements stored in each node.  The value 6 was chosen to make
// the size of node 64 bytes to match the cache line size.
const nodeSize = 6

// Make returns a UniqueList for the given slice.  Two calls to UniqueList with the same slice contents
// will return identical UniqueList objects.
func Make[T comparable](slice []T) UniqueList[T] {
	if len(slice) == 0 {
		return UniqueList[T]{}
	}

	var last unique.Handle[node[T]]
	l := 0

	// Iterate backwards through the lists in chunks of nodeSize, with the first chunk visited
	// being the partial chunk if the length of the slice is not a multiple of nodeSize.
	//
	// For each chunk, create an unrolled linked list node with a chunk of slice elements and a
	// pointer to the previously created node, uniquified through unique.Make.
	for chunk := range chunkReverse(slice, nodeSize) {
		var node node[T]
		copy(node.elements[:], chunk)
		node.next = last
		l += len(chunk)
		node.len = l
		last = unique.Make(node)
	}

	return UniqueList[T]{last}
}

// chunkReverse is similar to slices.Chunk, except that it returns the chunks in reverse
// order.  If the length of the slice is not a multiple of n then the first chunk returned
// (which is the last chunk of the input slice) is a partial chunk.
func chunkReverse[T any](slice []T, n int) iter.Seq[[]T] {
	return func(yield func([]T) bool) {
		l := len(slice)
		lastPartialChunkSize := l % n
		if lastPartialChunkSize > 0 {
			if !yield(slice[l-lastPartialChunkSize : l : l]) {
				return
			}
		}
		for i := l - lastPartialChunkSize - n; i >= 0; i -= n {
			if !yield(slice[i : i+n : i+n]) {
				return
			}
		}
	}
}
