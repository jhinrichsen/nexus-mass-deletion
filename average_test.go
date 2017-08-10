package main

import "testing"

func TestArithmeticMean(t *testing.T) {
	sample := []int{1, 3}
	want := 2
	got := ArithmeticMean(sample)
	if want != got {
		t.Fatalf("Want %v but got %v\n", want, got)
	}
}

func TestMedianOdd(t *testing.T) {
	sample := []int{2, 1, 3}
	want := 2
	got := Median(sample)
	if want != got {
		t.Fatalf("Want %v but got %v\n", want, got)
	}
}

func TestMedianEven(t *testing.T) {
	sample := []int{10, 1, 4, 6}
	want := 5
	got := Median(sample)
	if want != got {
		t.Fatalf("Want %v but got %v\n", want, got)
	}
}

func TestImmutableMedian(t *testing.T) {
	sample := []int{5, 1}
	want := 3
	got := Median(sample)
	if want != got {
		t.Fatalf("Want %v but got %v\n", want, got)
	}
	if sample[0] != 5 {
		t.Fatalf("Median mutated data, expected %v but got %v",
			[]int{5, 1},
			sample)
	}
}
