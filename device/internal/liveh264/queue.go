package liveh264

// Queue bounds undecoded network video independently of the decoder's pipe.
// A slow decoder must not turn TCP's receive buffer into seconds of stale
// pictures. On overflow, discard the queued GOP and resume only at an IDR.
// Push/Close have one producer; Frames has one consumer.
type Queue struct {
	frames  chan Frame
	waiting bool
}

// Retain one 30-picture GOP plus lookahead while OMX initializes. An eight-
// picture queue can overflow before the decoder has produced its first frame,
// repeatedly restarting it at the next IDR and preventing startup altogether.
// This remains bounded (at most 32 MiB with the absolute wire payload limit;
// approximately 250 KiB at the camera's configured 2 Mbit/s).
const QueuedFrames = 32

func NewQueue() *Queue                { return &Queue{frames: make(chan Frame, QueuedFrames)} }
func (q *Queue) Frames() <-chan Frame { return q.frames }
func (q *Queue) Close()               { close(q.frames) }

func (q *Queue) Push(frame Frame) {
	if !frame.valid() || q.waiting && !frame.Keyframe {
		return
	}
	select {
	case q.frames <- frame:
		q.waiting = false
		return
	default:
	}
	for {
		select {
		case <-q.frames:
		default:
			q.waiting = true
			if frame.Keyframe {
				q.frames <- frame
				q.waiting = false
			}
			return
		}
	}
}
