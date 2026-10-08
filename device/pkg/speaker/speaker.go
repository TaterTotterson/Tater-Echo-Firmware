package speaker

type Speaker interface {
	Init() error
	PumpPeriod(data []byte) error
	// EndStream marks the current audio stream as complete, so the driver
	// can distinguish "channel drained because playback finished" from a
	// mid-stream underrun.
	EndStream()
	// Flush discards queued-but-unplayed audio immediately (barge-in).
	Flush()

	// ── music plane ───────────────────────────────────────────────────────
	// A second, independent stream, mixed with the voice plane at the ALSA
	// write. It exists so a voice turn can DUCK music rather than pausing
	// it. Sendspin and local audio scenes feed this plane ahead of the DAC,
	// while replies and announcements use the independently duckable voice
	// plane.
	PumpMusic(data []byte) error
	EndMusicStream()
	// FlushMusic is for the user genuinely stopping or pausing. A voice turn
	// must duck instead — flushing throws away the buffered audio that makes
	// ducking instant, and on a non-seekable stream it cannot be recovered.
	FlushMusic()
	// SetDuck sets music attenuation in dB while voice plays (0 = none).
	SetDuck(db float64)

	Close()
}
