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
	"fmt"
	"slices"
	"testing"
)

func ExampleUniqueList() {
	a := []string{"a", "b", "c", "d"}
	uniqueA := Make(a)
	b := slices.Clone(a)
	uniqueB := Make(b)
	fmt.Println(uniqueA == uniqueB)
	fmt.Println(uniqueA.ToSlice())

	// Output: true
	// [a b c d]
}

func testSlice(n int) []int {
	var slice []int
	for i := 0; i < n; i++ {
		slice = append(slice, i)
	}
	return slice
}

func TestUniqueList(t *testing.T) {
	testCases := []struct {
		name string
		in   []int
	}{
		{
			name: "nil",
			in:   nil,
		},
		{
			name: "zero",
			in:   []int{},
		},
		{
			name: "one",
			in:   testSlice(1),
		},
		{
			name: "nodeSize_minus_one",
			in:   testSlice(nodeSize - 1),
		},
		{
			name: "nodeSize",
			in:   testSlice(nodeSize),
		},
		{
			name: "nodeSize_plus_one",
			in:   testSlice(nodeSize + 1),
		},
		{
			name: "two_times_nodeSize_minus_one",
			in:   testSlice(2*nodeSize - 1),
		},
		{
			name: "two_times_nodeSize",
			in:   testSlice(2 * nodeSize),
		},
		{
			name: "two_times_nodeSize_plus_one",
			in:   testSlice(2*nodeSize + 1),
		},
		{
			name: "large",
			in:   testSlice(1000),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			uniqueList := Make(testCase.in)

			if g, w := uniqueList.ToSlice(), testCase.in; !slices.Equal(g, w) {
				t.Errorf("incorrect ToSlice()\nwant: %q\ngot:  %q", w, g)
			}

			if g, w := slices.Collect(uniqueList.Iter()), testCase.in; !slices.Equal(g, w) {
				t.Errorf("incorrect Iter()\nwant: %q\ngot:  %q", w, g)
			}

			if g, w := uniqueList.AppendTo([]int{-1}), append([]int{-1}, testCase.in...); !slices.Equal(g, w) {
				t.Errorf("incorrect Iter()\nwant: %q\ngot:  %q", w, g)
			}

			if g, w := uniqueList.Len(), len(testCase.in); g != w {
				t.Errorf("incorrect Len(), want %v, got %v", w, g)
			}
		})
	}
}
