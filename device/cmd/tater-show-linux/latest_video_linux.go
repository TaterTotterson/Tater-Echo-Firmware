//go:build linux

package main

import "image"

// latestDecodedVideo separates display work from draining the hardware decoder.
// Only complete decoded pictures are replaceable; compressed prediction packets
// must still reach the decoder in order. The reader owns/reuses its input image,
// so this small pool copies pixels before returning to the decoder pipe.
type latestDecodedVideo struct {
	frames  chan *image.RGBA
	recycle chan *image.RGBA
}

func newLatestDecodedVideo(sharedRecycle ...chan *image.RGBA) *latestDecodedVideo {
	recycle := make(chan *image.RGBA, 3)
	if len(sharedRecycle) > 0 && sharedRecycle[0] != nil {
		recycle = sharedRecycle[0]
	}
	return &latestDecodedVideo{frames: make(chan *image.RGBA, 1), recycle: recycle}
}

func (q *latestDecodedVideo) release(frame *image.RGBA) {
	if frame == nil {
		return
	}
	select {
	case q.recycle <- frame:
	default:
	}
}

func (q *latestDecodedVideo) publish(source *image.RGBA) {
	if source == nil || source.Rect.Empty() {
		return
	}
	bounds := image.Rectangle{Max: source.Rect.Size()}
	var owned *image.RGBA
acquire:
	for {
		select {
		case candidate := <-q.recycle:
			if candidate.Rect == bounds {
				owned = candidate
				break acquire
			}
		default:
			owned = image.NewRGBA(bounds)
			break acquire
		}
	}
	for y := 0; y < bounds.Dy(); y++ {
		offset := source.PixOffset(source.Rect.Min.X, source.Rect.Min.Y+y)
		copy(owned.Pix[y*owned.Stride:y*owned.Stride+bounds.Dx()*4], source.Pix[offset:offset+bounds.Dx()*4])
	}
	select {
	case q.frames <- owned:
		return
	default:
	}
	select {
	case old := <-q.frames:
		q.release(old)
	default:
	}
	select {
	case q.frames <- owned:
	default:
		q.release(owned)
	}
}

func (q *latestDecodedVideo) discard() {
	for {
		select {
		case old := <-q.frames:
			q.release(old)
		default:
			return
		}
	}
}
