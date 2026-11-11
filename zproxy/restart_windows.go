package main

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
)

// restart runs exe with this process's arguments in the same console and exits with its
// exit code; it returns only when exe cannot be started.
func restart(exe string) error {
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	// Ctrl+C reaches both processes: leave it to the child, so the console stays until it ends.
	signal.Ignore(os.Interrupt)
	err := cmd.Wait()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.ExitCode())
	}
	os.Exit(0)
	return nil
}
