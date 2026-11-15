package main

import (
	"io"
	"os/exec"
	"sync/atomic"
	"time"
)

// The producer finishes its current framed input record, closes stdin, then
// signals writerDone. Drain stdout until the codec has observed EOF, emitted
// EOS, and completed Idle/Loaded teardown. Closing stdout first or immediately
// cancelling CommandContext can abort that hardware cleanup.
func finishEncoder(command *exec.Cmd, input io.Closer, output io.ReadCloser, writerDone <-chan struct{}, kill func(), budget time.Duration) (bool, error) {
	var forced atomic.Bool
	timer := time.AfterFunc(budget, func() {
		forced.Store(true)
		kill()
		_ = input.Close()
		_ = output.Close()
	})
	// Drain while the producer finishes: an encoder may need to publish its
	// previous output before it can consume the rest of the current raw frame.
	_, _ = io.Copy(io.Discard, output)
	<-writerDone
	err := command.Wait()
	timer.Stop()
	kill()
	_ = input.Close()
	_ = output.Close()
	return forced.Load(), err
}
