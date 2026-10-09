package speaker

import (
	"strings"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/mixer"
)

// Jack routing: the codec state each plug position needs.
//
// Here rather than in pcm_speaker.go because that file is ARM-only (build tag
// `server`) and the mapping is worth pinning on the host — it is a table of
// measured values, and a typo in one of them is silence rather than an error.
//
// MEASURED 2026-09-03 against a stock FireOS 5.5.5.4 Dot (root, no EchoMuse)
// driving the same cable, by diffing all 239 mixer controls across an insert
// on both devices. Stock changed five controls; this route reproduces the
// three whose effects have been measured and are needed by our mono path.

// The controls, by name: the internal driver's amp, the jack's output stage,
// and the DAC mux that feeds it.
const (
	ctlSpeakerAmp   = mixer.SpeakerAmp
	ctlHPDriverGain = mixer.HPDriverGain
	ctlDacMux       = mixer.DacMux
)

// HP driver gain values, as mixer indices on a 0..35 range that maps to
// -6dB..+29dB.
//
// hpGainInternal is what both a stock Dot and ours sit at with nothing
// plugged in. hpGainJack is stock's value with a plug in.
//
// The gap is the whole bug. Something drops this control to 0 — the FLOOR of
// the range, -6dB — when a plug goes in, and on a stock device the audio HAL
// then raises it to 11. We had nothing that did, so the external output sat at
// minimum gain: measured inaudible at 0, and audible immediately on writing
// 11 with music playing. It reads as "the jack does not work" rather than as
// "the jack is quiet", which is why it survived so long.
//
// NOT accdet, despite what this project's docs said for a month. Amazon's
// accdet driver (accdet_amzn.c) only reads a GPIO and calls switch_set_state
// on /sys/class/switch/h2w — it writes no mixer control and touches no codec
// register. Everything attributed to it is Android's audio HAL reacting to
// that switch state, which is also why it keeps happening: see
// reconcileJackRouting.
const (
	hpGainInternal = "6"
	hpGainJack     = "11"
)

// The DAC mux, #566. With a plug in, stock reads On and we read Off, and the
// jack sat 20-30dB below line level with HP Driver Gain already correct.
// Writing On restored stock's level (@Kozikodi, 2026-09-29, FireOS 5.5.5.4 on
// two Dots, diffed against stock playing the same cable). The board's DT has
// no extamp-dacmux pinctrl states, so AudDrv_GPIO_probe logs "fail -19" at
// boot on stock as well — the control works regardless.
//
// Stock also leaves it On with the jack empty. That case is NOT measured on
// the internal speaker, so removal writes Off: the state every Dot has played
// its speaker in until now.
const (
	dacMuxJack     = "On"
	dacMuxInternal = "Off"
)

// mixerWrite sets one control, by name.
type mixerWrite struct {
	Ctl  string
	Args []string
}

// jackRouting returns the mixer writes that put the codec into the state a
// given plug position needs.
//
// Three controls, deliberately. Stock also clears Right Channel Only and sets
// Ignore Ramp Up on insert, and NEITHER is copied here:
//
//   - Right Channel Only selects which codec channel carries the signal, and
//     our wire is mono — toStereo duplicates L into R — so both channels carry
//     the same samples whichever way it is set. It becomes real the day the
//     wire carries stereo, and not before.
//   - Ignore Ramp Up has not been measured on this hardware at all. Copying a
//     stock value whose effect is unknown is how you ship a change that cannot
//     be defended when it turns out to do something else.
//
// The order within a position does not matter: these are independent controls
// on a codec that is already clocking, not a sequence.
func jackRouting(inserted bool) []mixerWrite {
	if inserted {
		// Something is in the jack, so the internal driver must be silent —
		// otherwise the Dot plays to the room while the cable carries the
		// same audio somewhere else.
		return []mixerWrite{
			{Ctl: ctlSpeakerAmp, Args: []string{"Off"}},
			{Ctl: ctlHPDriverGain, Args: []string{hpGainJack, hpGainJack}},
			{Ctl: ctlDacMux, Args: []string{dacMuxJack}},
		}
	}
	// Nothing in the jack: the internal driver is the only output there is.
	return []mixerWrite{
		{Ctl: ctlSpeakerAmp, Args: []string{"On"}},
		{Ctl: ctlHPDriverGain, Args: []string{hpGainInternal, hpGainInternal}},
		{Ctl: ctlDacMux, Args: []string{dacMuxInternal}},
	}
}

// Radar and Spot share the DAC family with Biscuit, but not its complete
// analog speaker path. Radar follows the hardware-tested echolocal path; Rook
// follows TECHO5's route measured against the Spot Fire OS audio_device.xml.
func jackRoutingForTarget(target string, inserted bool) []mixerWrite {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "radar" {
		if inserted {
			return []mixerWrite{
				{Ctl: mixer.InternalSpeakerAmp, Args: []string{"Off"}},
				{Ctl: "MFP Gpio Mute", Args: []string{"Off"}},
				{Ctl: ctlSpeakerAmp, Args: []string{"On"}},
				{Ctl: "Headphone_Speaker_Mux", Args: []string{"Headphone"}},
				{Ctl: "Ignore Ramp Up", Args: []string{"On"}},
				{Ctl: ctlHPDriverGain, Args: []string{hpGainJack, hpGainJack}},
				{Ctl: ctlDacMux, Args: []string{dacMuxJack}},
				{Ctl: "Right Channel Only", Args: []string{"Off"}},
			}
		}
		return []mixerWrite{
			{Ctl: mixer.InternalSpeakerAmp, Args: []string{"On"}},
			{Ctl: "MFP Gpio Mute", Args: []string{"Off"}},
			{Ctl: ctlSpeakerAmp, Args: []string{"On"}},
			{Ctl: "Headphone_Speaker_Mux", Args: []string{"Speaker"}},
			{Ctl: "Ignore Ramp Up", Args: []string{"Off"}},
			{Ctl: ctlHPDriverGain, Args: []string{hpGainInternal, hpGainInternal}},
			{Ctl: ctlDacMux, Args: []string{dacMuxInternal}},
			{Ctl: "Right Channel Only", Args: []string{"On"}},
		}
	}
	if target != "rook" {
		return jackRouting(inserted)
	}
	if inserted {
		return []mixerWrite{
			{Ctl: ctlSpeakerAmp, Args: []string{"Off"}},
			{Ctl: "Audio_LineOut_Setting", Args: []string{"Off"}},
			{Ctl: "Ignore Ramp Up", Args: []string{"On"}},
			{Ctl: ctlHPDriverGain, Args: []string{"11", "11"}},
			{Ctl: "Right Channel Only", Args: []string{"Off"}},
		}
	}
	return []mixerWrite{
		{Ctl: "Audio_LineOut_Setting", Args: []string{"Off"}},
		{Ctl: "Ignore Ramp Up", Args: []string{"Off"}},
		{Ctl: "Right Channel Only", Args: []string{"On"}},
		{Ctl: ctlHPDriverGain, Args: []string{"9", "9"}},
		{Ctl: "Amp Fault Enable", Args: []string{"On"}},
		{Ctl: ctlSpeakerAmp, Args: []string{"On"}},
	}
}

// ── Drift ────────────────────────────────────────────────────────────────────
//
// Applying the routing once on a jack edge is not enough, and this is measured
// rather than anticipated. Android's audio HAL rewrites the codec whenever
// mediaserver restarts, which on a device holding pcm23p with a plug inserted
// is roughly every 60-90 seconds. Observed directly on 2026-09-03: HP Driver
// Gain set to 11 came back as 0 within a minute, every minute, and each revert
// coincided exactly with mediaserver taking a new pid. So the jack works for
// about a minute after a plug event and then goes quiet again.
//
// We cannot stop it — mediaserver publishes AudioFlinger and AudioPolicyService
// and system_server crash-loops without them, taking WiFi down with it (tested,
// 2026-09-03). So the routing is reconciled instead: read the controls back,
// and rewrite only the ones that moved.

// jackRoutingDrift returns the writes needed to bring the codec back to the
// state `inserted` requires, given what the controls currently read.
//
// `current` maps control name to the value read back. A control MISSING from
// the map is skipped rather than rewritten: an unreadable control means the
// read failed, and "failure to look is not evidence of absence" applies here
// exactly as it does to the controller's asset reconcile — rewriting on a
// failed read would rewrite it every interval forever.
func jackRoutingDrift(inserted bool, current map[string]string) []mixerWrite {
	return jackRoutingDriftForTarget("biscuit", inserted, current)
}

func jackRoutingDriftForTarget(target string, inserted bool, current map[string]string) []mixerWrite {
	var out []mixerWrite
	for _, w := range jackRoutingForTarget(target, inserted) {
		got, ok := current[w.Ctl]
		if !ok {
			continue
		}
		if got != w.Args[0] {
			out = append(out, w)
		}
	}
	return out
}
