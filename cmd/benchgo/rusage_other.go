//go:build !linux

package main

// rusage não é medido fora do Linux (o benchmark oficial roda em container).
func rusage() (user, sys float64, maxRSSKB int64) { return 0, 0, 0 }
