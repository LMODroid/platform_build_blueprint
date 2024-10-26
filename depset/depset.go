// Copyright 2020 Google Inc. All rights reserved.
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

package depset

import (
	"fmt"
	"iter"
	"slices"
	"unique"

	"github.com/google/blueprint/gobtools"
	"github.com/google/blueprint/uniquelist"
)

// DepSet is designed to be conceptually compatible with Bazel's depsets:
// https://docs.bazel.build/versions/master/skylark/depsets.html

type Order int

const (
	PREORDER Order = iota
	POSTORDER
	TOPOLOGICAL
)

func (o Order) String() string {
	switch o {
	case PREORDER:
		return "PREORDER"
	case POSTORDER:
		return "POSTORDER"
	case TOPOLOGICAL:
		return "TOPOLOGICAL"
	default:
		panic(fmt.Errorf("Invalid Order %d", o))
	}
}

type depSettableType comparable

// A DepSet efficiently stores a slice of an arbitrary type from transitive dependencies without
// copying. It is stored as a DAG of DepSet nodes, each of which has some direct contents and a list
// of dependency DepSet nodes.
//
// A DepSet has an order that will be used to walk the DAG when ToList() is called.  The order
// can be POSTORDER, PREORDER, or TOPOLOGICAL.  POSTORDER and PREORDER orders return a postordered
// or preordered left to right flattened list.  TOPOLOGICAL returns a list that guarantees that
// elements of children are listed after all of their parents (unless there are duplicate direct
// elements in the DepSet or any of its transitive dependencies, in which case the ordering of the
// duplicated element is not guaranteed).
//
// A DepSet is created by New or NewBuilder.Build from the slice for direct contents
// and the DepSets of dependencies. A DepSet is immutable once created.
//
// DepSets are stored using UniqueList which uses the unique package to intern them, which ensures
// that the graph semantics of the DepSet are maintained even after serializing/deserializing or
// when mixing newly created and deserialized DepSets.
type DepSet[T depSettableType] struct {
	// handle is a unique.Handle to an internal depSet object, which makes DepSets effectively a
	// single pointer.
	handle unique.Handle[depSet[T]]
}

type depSet[T depSettableType] struct {
	preorder   bool
	reverse    bool
	order      Order
	direct     uniquelist.UniqueList[T]
	transitive uniquelist.UniqueList[DepSet[T]]
}

// impl returns a copy of the uniquified  depSet for a DepSet.
func (d DepSet[T]) impl() depSet[T] {
	return d.handle.Value()
}

func (d DepSet[T]) order() Order {
	impl := d.impl()
	return impl.order
}

type depSetGob[T depSettableType] struct {
	Preorder   bool
	Reverse    bool
	Order      Order
	Direct     []T
	Transitive []DepSet[T]
}

func (d *DepSet[T]) ToGob() *depSetGob[T] {
	impl := d.impl()
	return &depSetGob[T]{
		Preorder:   impl.preorder,
		Reverse:    impl.reverse,
		Order:      impl.order,
		Direct:     impl.direct.ToSlice(),
		Transitive: impl.transitive.ToSlice(),
	}
}

func (d *DepSet[T]) FromGob(data *depSetGob[T]) {
	d.handle = unique.Make(depSet[T]{
		preorder:   data.Preorder,
		reverse:    data.Reverse,
		order:      data.Order,
		direct:     uniquelist.Make(data.Direct),
		transitive: uniquelist.Make(data.Transitive),
	})
}

func (d *DepSet[T]) GobEncode() ([]byte, error) {
	return gobtools.CustomGobEncode[depSetGob[T]](d)
}

func (d *DepSet[T]) GobDecode(data []byte) error {
	return gobtools.CustomGobDecode[depSetGob[T]](data, d)
}

// New returns an immutable DepSet with the given order, direct and transitive contents.
func New[T depSettableType](order Order, direct []T, transitive []DepSet[T]) DepSet[T] {
	var directCopy []T
	var transitiveCopy []DepSet[T]

	// Create a zero value of DepSet, which will be used to check if the unique.Handle is the zero value.
	var zeroDepSet DepSet[T]

	nonEmptyTransitiveCount := 0
	for _, t := range transitive {
		// A zero valued DepSet has no associated unique.Handle for a depSet.  It has no contents, so it can
		// be skipped.
		if t != zeroDepSet {
			if t.handle.Value().order != order {
				panic(fmt.Errorf("incompatible order, new DepSet is %s but transitive DepSet is %s",
					order, t.handle.Value().order))
			}
			nonEmptyTransitiveCount++
		}
	}

	directCopy = slices.Clone(direct)
	if nonEmptyTransitiveCount > 0 {
		transitiveCopy = make([]DepSet[T], 0, nonEmptyTransitiveCount)
	}
	var transitiveIter iter.Seq2[int, DepSet[T]]
	if order == TOPOLOGICAL {
		// TOPOLOGICAL is implemented as a postorder traversal followed by reversing the output.
		// Pre-reverse the inputs here so their order is maintained in the output.
		slices.Reverse(directCopy)
		transitiveIter = slices.Backward(transitive)
	} else {
		transitiveIter = slices.All(transitive)
	}

	// Copy only the non-zero-valued elements in the transitive list.  transitiveIter may be a forwards
	// or backards iterator.
	for _, t := range transitiveIter {
		if t != zeroDepSet {
			transitiveCopy = append(transitiveCopy, t)
		}
	}

	// Optimization:  If both the direct and transitive lists are empty then this DepSet is semantically
	// equivalent to the zero valued DepSet (effectively a nil pointer).  Returning the zero value will
	// allow this DepSet to be skipped in DepSets that reference this one as a transitive input, saving
	// memory.
	if len(directCopy) == 0 && len(transitive) == 0 {
		return DepSet[T]{}
	}

	// Create a depSet to hold the contents.
	depSet := depSet[T]{
		preorder:   order == PREORDER,
		reverse:    order == TOPOLOGICAL,
		order:      order,
		direct:     uniquelist.Make(directCopy),
		transitive: uniquelist.Make(transitiveCopy),
	}

	// Uniquify the depSet and store it in a DepSet.
	return DepSet[T]{unique.Make(depSet)}
}

// Builder is used to create an immutable DepSet.
type Builder[T depSettableType] struct {
	order      Order
	direct     []T
	transitive []DepSet[T]
}

// NewBuilder returns a Builder to create an immutable DepSet with the given order and
// type, represented by a slice of type that will be in the DepSet.
func NewBuilder[T depSettableType](order Order) *Builder[T] {
	return &Builder[T]{
		order: order,
	}
}

// DirectSlice adds direct contents to the DepSet being built by a Builder. Newly added direct
// contents are to the right of any existing direct contents.
func (b *Builder[T]) DirectSlice(direct []T) *Builder[T] {
	b.direct = append(b.direct, direct...)
	return b
}

// Direct adds direct contents to the DepSet being built by a Builder. Newly added direct
// contents are to the right of any existing direct contents.
func (b *Builder[T]) Direct(direct ...T) *Builder[T] {
	b.direct = append(b.direct, direct...)
	return b
}

// Transitive adds transitive contents to the DepSet being built by a Builder. Newly added
// transitive contents are to the right of any existing transitive contents.
func (b *Builder[T]) Transitive(transitive ...DepSet[T]) *Builder[T] {
	var zeroDepSet DepSet[T]
	for _, t := range transitive {
		if t != zeroDepSet && t.order() != b.order {
			panic(fmt.Errorf("incompatible order, new DepSet is %s but transitive DepSet is %s",
				b.order, t.order()))
		}
	}
	b.transitive = append(b.transitive, transitive...)
	return b
}

// Build returns the DepSet being built by this Builder.  The Builder retains its contents
// for creating more depSets.
func (b *Builder[T]) Build() DepSet[T] {
	return New(b.order, b.direct, b.transitive)
}

// collect collects the contents of the DepSet in depth-first order, preordered if d.preorder is set,
// otherwise postordered.
func (d DepSet[T]) collect() []T {
	visited := make(map[DepSet[T]]bool)
	var list []T

	var dfs func(d DepSet[T])
	dfs = func(d DepSet[T]) {
		impl := d.impl()
		visited[d] = true
		if impl.preorder {
			list = impl.direct.AppendTo(list)
		}
		for dep := range impl.transitive.Iter() {
			if !visited[dep] {
				dfs(dep)
			}
		}

		if !impl.preorder {
			list = impl.direct.AppendTo(list)
		}
	}

	dfs(d)

	return list
}

// ToList returns the DepSet flattened to a list.  The order in the list is based on the order
// of the DepSet.  POSTORDER and PREORDER orders return a postordered or preordered left to right
// flattened list.  TOPOLOGICAL returns a list that guarantees that elements of children are listed
// after all of their parents (unless there are duplicate direct elements in the DepSet or any of
// its transitive dependencies, in which case the ordering of the duplicated element is not
// guaranteed).
func (d DepSet[T]) ToList() []T {
	var zeroDepSet unique.Handle[depSet[T]]
	if d.handle == zeroDepSet {
		return nil
	}
	impl := d.impl()
	list := d.collect()
	list = firstUniqueInPlace(list)
	if impl.reverse {
		slices.Reverse(list)
	}
	return list
}

// firstUniqueInPlace returns all unique elements of a slice, keeping the first copy of
// each.  It modifies the slice contents in place, and returns a subslice of the original
// slice.
func firstUniqueInPlace[T comparable](slice []T) []T {
	// 128 was chosen based on BenchmarkFirstUniqueStrings results.
	if len(slice) > 128 {
		return firstUniqueMap(slice)
	}
	return firstUniqueList(slice)
}

// firstUniqueList is an implementation of firstUnique using an O(N^2) list comparison to look for
// duplicates.
func firstUniqueList[T any](in []T) []T {
	writeIndex := 0
outer:
	for readIndex := 0; readIndex < len(in); readIndex++ {
		for compareIndex := 0; compareIndex < writeIndex; compareIndex++ {
			if interface{}(in[readIndex]) == interface{}(in[compareIndex]) {
				// The value at readIndex already exists somewhere in the output region
				// of the slice before writeIndex, skip it.
				continue outer
			}
		}
		if readIndex != writeIndex {
			in[writeIndex] = in[readIndex]
		}
		writeIndex++
	}
	return in[0:writeIndex]
}

// firstUniqueMap is an implementation of firstUnique using an O(N) hash set lookup to look for
// duplicates.
func firstUniqueMap[T comparable](in []T) []T {
	writeIndex := 0
	seen := make(map[T]bool, len(in))
	for readIndex := 0; readIndex < len(in); readIndex++ {
		if _, exists := seen[in[readIndex]]; exists {
			continue
		}
		seen[in[readIndex]] = true
		if readIndex != writeIndex {
			in[writeIndex] = in[readIndex]
		}
		writeIndex++
	}
	return in[0:writeIndex]
}
