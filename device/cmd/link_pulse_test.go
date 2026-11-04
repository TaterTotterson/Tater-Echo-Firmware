//go:build server

package main

import (
	"context"
	"testing"
)

func TestCancelPulseAndWaitIsAPaintBarrier(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	cancelObserved := make(chan struct{})
	allowFinalPaint := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(cancelObserved)
		<-allowFinalPaint
		close(done)
	}()

	returned := make(chan struct{})
	go func() {
		cancelPulseAndWait(cancel, done)
		close(returned)
	}()

	<-cancelObserved
	select {
	case <-returned:
		t.Fatal("pulse stop returned before the in-flight frame completed")
	default:
	}
	close(allowFinalPaint)
	<-returned
}
