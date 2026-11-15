//go:build linux

package main

import (
	"image"
	"sync"
	"testing"
)

func TestLatestDecodedVideoOwnsOnlyNewestPicture(t *testing.T) {
	q := newLatestDecodedVideo()
	parent := image.NewRGBA(image.Rect(-2, -2, 20, 20))
	source := parent.SubImage(image.Rect(2, 3, 10, 11)).(*image.RGBA)
	for i := 0; i < 100; i++ {
		for y := source.Rect.Min.Y; y < source.Rect.Max.Y; y++ {
			for x := source.Rect.Min.X; x < source.Rect.Max.X; x++ {
				source.Pix[source.PixOffset(x, y)] = byte(i)
			}
		}
		q.publish(source)
	}
	clear(parent.Pix)
	if len(q.frames) != 1 {
		t.Fatal("decoded video did not remain one-deep")
	}
	latest := <-q.frames
	if latest.Rect != image.Rect(0, 0, 8, 8) {
		t.Fatal("subimage dimensions changed", latest.Rect)
	}
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if latest.Pix[latest.PixOffset(x, y)] != 99 {
				t.Fatal("borrowed frame mutated or stale frame retained")
			}
		}
	}
	q.release(latest)
	q.publish(image.NewRGBA(image.Rect(0, 0, 4, 2)))
	changed := <-q.frames
	if changed.Rect != image.Rect(0, 0, 4, 2) {
		t.Fatal("geometry change reused wrong buffer")
	}
	q.release(changed)
	q.publish(nil)
	q.publish(image.NewRGBA(image.Rectangle{}))
	if len(q.frames) != 0 {
		t.Fatal("invalid source published")
	}
}

func TestLatestDecodedVideoDoesNotReuseConsumerOwnedPixels(t *testing.T) {
	q := newLatestDecodedVideo()
	source := image.NewRGBA(image.Rect(0, 0, 16, 16))
	source.Pix[0] = 42
	q.publish(source)
	consumer := <-q.frames
	for i := 0; i < 1000; i++ {
		source.Pix[0] = byte(i)
		q.publish(source)
	}
	if consumer.Pix[0] != 42 {
		t.Fatal("slow display's owned frame was overwritten")
	}
	q.release(consumer)
	q.discard()
	if len(q.frames) != 0 || len(q.recycle) > 3 {
		t.Fatal("frame pool or pending queue unbounded")
	}
}

func TestLatestDecodedVideoConcurrentReaderAndSlowDisplay(t *testing.T) {
	q := newLatestDecodedVideo()
	done := make(chan struct{})
	var display sync.WaitGroup
	display.Add(1)
	go func() {
		defer display.Done()
		for {
			select {
			case <-done:
				return
			case frame := <-q.frames:
				for i := 0; i < len(frame.Pix); i += 4 {
					_ = frame.Pix[i]
				}
				q.release(frame)
			}
		}
	}()
	source := image.NewRGBA(image.Rect(0, 0, 128, 72))
	for i := 0; i < 2000; i++ {
		source.Pix[0] = byte(i)
		q.publish(source)
	}
	close(done)
	display.Wait()
	q.discard()
	if len(q.frames) != 0 {
		t.Fatal("pending picture was not released")
	}
}
