//go:build !windows

package main

import (
	"os"
	"syscall"
)

// restart replaces this process with exe, same arguments; it returns only on failure.
func restart(exe string) error {
	return syscall.Exec(exe, os.Args, os.Environ())
}
