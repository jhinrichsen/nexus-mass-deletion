// Provide some performance metrics. Everything is computed based on integer
// arithmetic, what's all this float stuff, anyhow?
// http://docs.oracle.com/cd/E19957-01/806-3568/ncg_goldberg.html
// Early Forth did not include floating point arithmetic, and they did like
// control telescopes.

package main

import (
	"sort"
)

// ArithmeticMean does not perform any bound or overflow checking
func ArithmeticMean(is []int) int {
	sum := 0
	for _, v := range is {
		sum += v
	}
	return sum / len(is)
}

func Median(slice []int) int {
	// sorting is in-place, create copy so that input is not mutated
	is := append([]int(nil), slice...)
	sort.Ints(is)
	n := len(is)
	if even(n) {
		upper := n / 2
		return ArithmeticMean([]int{is[upper-1], is[upper]})
	}
	return is[n/2]
}

func even(i int) bool {
	return i%2 == 0
}
