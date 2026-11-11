//go:build linux

package main

import "syscall"

// rusage retorna CPU de usuário/sistema (s) e pico de RSS (KiB no Linux).
func rusage() (user, sys float64, maxRSSKB int64) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, 0, 0
	}
	tv := func(t syscall.Timeval) float64 { return float64(t.Sec) + float64(t.Usec)/1e6 }
	return tv(ru.Utime), tv(ru.Stime), ru.Maxrss
}
