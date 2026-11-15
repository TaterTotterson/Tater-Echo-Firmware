package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Use a fresh copy of the test executable so these lifecycle tests need no
// camera hardware, vendor runtime, shell utilities, or network access.
func TestEncoderLifecycleChild(t *testing.T) {
	mode := os.Getenv("TATER_TEST_ENCODER_LIFECYCLE")
	if mode == "" {
		return
	}
	if mode == "stall" {
		time.Sleep(30 * time.Second)
		os.Exit(3)
	}
	// Fill stdout before reading input. The parent must drain output while
	// its producer finishes a frame larger than the stdin pipe capacity.
	if _, err := os.Stdout.Write(bytes.Repeat([]byte{0x5a}, 256*1024)); err != nil {
		os.Exit(4)
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil || len(data) != 256*1024+16 {
		os.Exit(5)
	}
	if _, err := os.Stdout.Write([]byte("clean-eos-idle-loaded")); err != nil {
		os.Exit(6)
	}
	os.Exit(0)
}

func TestFinishEncoderDrainsAndPreservesWholeInputRecord(t *testing.T) {
	ctx, kill := context.WithCancel(context.Background())
	defer kill()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEncoderLifecycleChild$")
	command.Env = append(os.Environ(), "TATER_TEST_ENCODER_LIFECYCLE=graceful")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	writerDone := make(chan struct{})
	writerErr := make(chan error, 1)
	go func() {
		defer close(writerDone)
		defer input.Close()
		_, err := input.Write(bytes.Repeat([]byte{0x33}, 256*1024+16))
		writerErr <- err
	}()
	forced, err := finishEncoder(command, input, output, writerDone, kill, 5*time.Second)
	if forced || err != nil {
		t.Fatalf("normal stop did not complete cleanly: forced=%v err=%v", forced, err)
	}
	if err := <-writerErr; err != nil {
		t.Fatalf("truncated the producer's framed input: %v", err)
	}
	if command.ProcessState == nil || !command.ProcessState.Success() {
		t.Fatal("encoder was not reaped successfully")
	}
}

func TestFinishEncoderBoundsStalledProducerAndProcess(t *testing.T) {
	ctx, kill := context.WithCancel(context.Background())
	defer kill()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEncoderLifecycleChild$")
	command.Env = append(os.Environ(), "TATER_TEST_ENCODER_LIFECYCLE=stall")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		defer input.Close()
		_, _ = input.Write(make([]byte, 256*1024))
	}()
	started := time.Now()
	forced, err := finishEncoder(command, input, output, writerDone, kill, 100*time.Millisecond)
	if !forced || err == nil || command.ProcessState == nil || command.ProcessState.Success() {
		t.Fatalf("stalled encoder was not forcibly reaped: forced=%v err=%v", forced, err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("stalled encoder exceeded the bounded fallback")
	}
}
