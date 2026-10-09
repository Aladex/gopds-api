package main

import (
	"os"
	"os/signal"
	"syscall"
)

// shutdownSignals stop the server gracefully: Ctrl-C, and SIGTERM, which a
// container runtime sends to stop a pod. Without SIGTERM here the default
// action kills the process and no deferred shutdown runs.
var shutdownSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

// notifyShutdown returns a channel that receives the first shutdown signal.
// main waits on it; the signal tests drive the same registration.
func notifyShutdown() <-chan os.Signal {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, shutdownSignals...)
	return quit
}
