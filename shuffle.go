package main

import "math/rand"

// Shuffle uses Fisher-Yates to in-place shuffle
// rand.Seed() is not initialized
func Shuffle(a []string) {
	for i := range a {
		j := rand.Intn(i + 1)
		a[i], a[j] = a[j], a[i]
	}
}
