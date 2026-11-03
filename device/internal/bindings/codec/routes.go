// Package codec brings up the DAPM routes the audio path needs, instead of
// inheriting them from Amazon's audio HAL.
//
// Until 2026-09-04 nothing here existed, because nothing had to: on FireOS the
// HAL configures the codec long before our process opens a PCM, so both the
// microphone array and the speaker worked and we never learned we were relying
// on it. Running native userspace with no Android services made the
// dependency visible in the least helpful way available — capture returned a
// steady rms≈0.00035 with a perfectly healthy ALSA clock (300.6s of audio over
// 300.3s of wall, zero stalls), and playback reported
// "voice stream complete, periods=35 underruns=0" while the room stayed silent.
//
// Both halves had the same cause. An ASoC route that is not connected leaves
// DAPM no reason to power the converter at either end of it, so the hardware is
// powered DOWN rather than merely misrouted. Read off the codec (i2c 2-0018) on
// a device with no Android, against a stock FireOS Dot running the same
// firmware:
//
//	        stock   ours     meaning
//	0012      85      05     NADC clock divider, bit7 = powered
//	0013      83      03     MADC clock divider
//	0026      11      00     ADC flags: left+right converting
//	003f      d6      16     DAC data path, bit7/6 = left/right powered
//	0089      30      00     output driver power
//	008c/8d   08      00     HPL/HPR output mixer routing
//
// Applying the routes below took every one of those to stock's exact value.
//
// This is written unconditionally, on FireOS as well, and that is deliberate:
// the values are the ones the HAL would set anyway, so a device that still has
// Android loses nothing, and one that does not gains a working audio path. The
// point of the project is not to need Amazon's userspace, and inheriting
// hardware state from it is a dependency whether or not it currently holds.
package codec

import (
	"log"
	"strings"
	"sync"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/bindings/mixer"
)

// Write sets one mixer control, found by name.
type Write struct {
	Name  string
	Value string
}

// Routes is every DAPM switch that must be closed for audio to flow.
//
// By NAME, never by control id (#546). These were ids until 2026-09-17, and on
// the FireOS 6 kernel every one of them named a different control: the eight
// capture writes closed the single-ended IN2 inputs, and the two playback
// writes hit input-mixer switches. Each was a valid write, so nothing failed.
//
// CAPTURE: the microphone array reaches the codec on the DIFFERENTIAL inputs,
// not the single-ended ones. Nothing routed DIF1 into any of the four ADCs, so
// all four sat powered down. The neighbouring "ADC_x DIF1_L/R Input Gain"
// controls are a DIFFERENT thing in the same register block: they do not route
// anything, but they do set capture level. They are applied separately below
// so a failed route (silence) is distinguishable from a failed gain write
// (about 6dB low).
//
// PLAYBACK: the DAC was not connected to the output mixer, so it powered down
// with the firmware streaming correctly into it.
var Routes = []Write{
	{"ADC_D Right Ip Select ADC_D DIF1_R switch", "1"},
	{"ADC_D Left Ip Select ADC_D DIF1_L switch", "1"},
	{"ADC_C Right Ip Select ADC_C DIF1_R switch", "1"},
	{"ADC_C Left Ip Select ADC_C DIF1_L switch", "1"},
	{"ADC_B Right Ip Select ADC_B DIF1_R switch", "1"},
	{"ADC_B Left Ip Select ADC_B DIF1_L switch", "1"},
	{"ADC_A Right Ip Select ADC_A DIF1_R switch", "1"},
	{"ADC_A Left Ip Select ADC_A DIF1_L switch", "1"},

	{"HPR Output Mixer R_DAC Switch", "1"},
	{"HPL Output Mixer L_DAC Switch", "1"},
}

// adcInputGains returns the differential-input level switches for the ADCs a
// board actually has. Amazon's audio HAL leaves these Off. Without Android the
// kernel default remains On, which measured about 5-6dB quieter than FireOS on
// otherwise-identical hardware. "0" is the tinymix value for Off.
//
// Keep these target-specific: writing A-D unconditionally would turn a useful
// calibration check into four or six expected "control not found" failures on
// the one- and two-ADC boards.
func adcInputGains(adcs ...string) []Write {
	writes := make([]Write, 0, len(adcs)*2)
	for _, adc := range adcs {
		writes = append(writes,
			Write{"ADC_" + adc + " DIF1_L Input Gain", "0"},
			Write{"ADC_" + adc + " DIF1_R Input Gain", "0"},
		)
	}
	return writes
}

var (
	biscuitInputGains  = adcInputGains("A", "B", "C", "D")
	radarInputGains    = adcInputGains("A", "B", "C", "D")
	checkersInputGains = adcInputGains("A")
	cronosInputGains   = adcInputGains("A")
	crownInputGains    = adcInputGains("A", "B")
	rookInputGains     = adcInputGains("A", "B")
)

func inputGainsForTarget(target string) []Write {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "radar":
		return radarInputGains
	case "checkers":
		return checkersInputGains
	case "cronos":
		return cronosInputGains
	case "crown":
		return crownInputGains
	case "rook":
		return rookInputGains
	case "athena":
		return nil
	default:
		return biscuitInputGains
	}
}

// Radar shares Puffin's four-ADC capture topology and HPL/HPR DAC route with
// Biscuit, but its TLV320AIC3204 drives a separate external amplifier gate on
// MFP2. Keep that gate muted while the route and filter are programmed; the
// speaker opens the external amp onto clocked silence and releases MFP2 last.
// Radar's speaker path uses both DAC channels (Right Channel Only Off).
var RadarRoutes = []Write{
	{"ADC_D Right Ip Select ADC_D DIF1_R switch", "1"},
	{"ADC_D Left Ip Select ADC_D DIF1_L switch", "1"},
	{"ADC_C Right Ip Select ADC_C DIF1_R switch", "1"},
	{"ADC_C Left Ip Select ADC_C DIF1_L switch", "1"},
	{"ADC_B Right Ip Select ADC_B DIF1_R switch", "1"},
	{"ADC_B Left Ip Select ADC_B DIF1_L switch", "1"},
	{"ADC_A Right Ip Select ADC_A DIF1_R switch", "1"},
	{"ADC_A Left Ip Select ADC_A DIF1_L switch", "1"},

	{"HPR Output Mixer R_DAC Switch", "1"},
	{"HPL Output Mixer L_DAC Switch", "1"},
	{"MFP Gpio Mute", "On"},
	{"Audio_DacMux_Setting", "Off"},
	{"Ignore Ramp Up", "Off"},
	{"DRC Control", "Disabled"},
	{"HP Driver Gain Volume", "6"},
	{"Right Channel Only", "Off"},
}

// radarFlatEQ leaves all six programmable biquads flat and keeps the codec's
// required closing block. This is the hardware-tested Tater-native Radar
// configuration; output shaping and protection happen in the same software
// output chain used by the Shows.
var radarFlatEQ = []byte{
	128, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	128, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	128, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	128, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	128, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	128, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	127, 247, 0, 0, 128, 9, 0, 0, 127, 239, 0, 0, 0, 17, 0, 0,
	0, 17, 0, 0, 127, 222, 0, 0, 15, 0, 0,
}

// CheckersRoutes is the Fire OS 6574.1 audio_device.xml route verified
// against the live Checkers mixer and playback trace. Checkers has one
// TLV320AIC3101 ADC and an RT5616 DAC; applying Biscuit's four-ADC / HPL-HPR
// route would leave both ends silent.
var CheckersRoutes = []Write{
	{"ADC_A Left Ip Select ADC_A DIF1_L switch", "1"},
	{"ADC_A Right Ip Select ADC_A DIF1_R switch", "1"},
	{"ADC_A MICPGA Volume Ctrl", "40"},
	{"SpiTimeStamps", "Off"},

	{"Audio_I2S0dl1_hd_Switch", "On"},
	{"DAC MIXL INF1 Switch", "1"},
	{"DAC MIXR INF1 Switch", "1"},
	{"Stereo DAC MIXL DAC L1 Switch", "1"},
	{"Stereo DAC MIXL DAC R1 Switch", "1"},
	{"Stereo DAC MIXR DAC L1 Switch", "1"},
	{"Stereo DAC MIXR DAC R1 Switch", "1"},
	{"OUT MIXL DAC L1 Switch", "1"},
	{"OUT MIXR DAC R1 Switch", "1"},
	{"LOUT MIX OUTVOL L Switch", "1"},
	{"LOUT MIX OUTVOL R Switch", "1"},
	{"OUT Channel Switch", "1"},
	{"OUT Playback Switch", "1"},
}

// Cronos keeps the amplifier path configured by its kernel. It has the same
// AIC3101 microphone converter and four-channel mic-plus-loopback frame as
// Checkers, but its speaker is either a MAX98396 or TAS5805M rather than the
// RT5616/GPIO path used by Checkers.
var CronosRoutes = []Write{
	{"ADC_A Left Ip Select ADC_A DIF1_L switch", "1"},
	{"ADC_A Right Ip Select ADC_A DIF1_R switch", "1"},
	{"ADC_A MICPGA Volume Ctrl", "40"},
	{"ADC_A Left Mute", "Off"},
	{"ADC_A Right Mute", "Off"},
	{"SpiTimeStamps", "Off"},
}

// CrownRoutes is the Echo Show 8 first-generation path measured by TECHO5 on
// real Crown hardware. Playback is Checkers' RT5616 line-out path; capture has
// a second AIC3101 ADC because Crown exposes four microphones followed by two
// playback-loopback channels on pcm22c.
var CrownRoutes = []Write{
	{"ADC_A Left Ip Select ADC_A DIF1_L switch", "1"},
	{"ADC_A Right Ip Select ADC_A DIF1_R switch", "1"},
	{"ADC_B Left Ip Select ADC_B DIF1_L switch", "1"},
	{"ADC_B Right Ip Select ADC_B DIF1_R switch", "1"},
	{"ADC_A MICPGA Volume Ctrl", "40"},
	{"ADC_B MICPGA Volume Ctrl", "40"},
	{"ADC_A Left Mute", "Off"},
	{"ADC_A Right Mute", "Off"},
	{"ADC_B Left Mute", "Off"},
	{"ADC_B Right Mute", "Off"},
	{"SpiTimeStamps", "Off"},

	{"Audio_I2S0dl1_hd_Switch", "On"},
	{"DAC MIXL INF1 Switch", "1"},
	{"DAC MIXR INF1 Switch", "1"},
	{"Stereo DAC MIXL DAC L1 Switch", "1"},
	{"Stereo DAC MIXL DAC R1 Switch", "1"},
	{"Stereo DAC MIXR DAC L1 Switch", "1"},
	{"Stereo DAC MIXR DAC R1 Switch", "1"},
	{"OUT MIXL DAC L1 Switch", "1"},
	{"OUT MIXR DAC R1 Switch", "1"},
	{"LOUT MIX OUTVOL L Switch", "1"},
	{"LOUT MIX OUTVOL R Switch", "1"},
	{"OUT Channel Switch", "1"},
	{"OUT Playback Switch", "1"},
}

// Rook has two TLV320AIC3101 microphone ADCs and the TLV320AIC32x4 speaker
// codec. Unlike Biscuit's four ADCs or Checkers' RT5616 DAC, only A/B and the
// Dot-style HPL/HPR output routes are present (TECHO5 Spot's measured path).
var RookRoutes = []Write{
	{"ADC_A Left Ip Select ADC_A DIF1_L switch", "1"},
	{"ADC_A Right Ip Select ADC_A DIF1_R switch", "1"},
	{"ADC_B Left Ip Select ADC_B DIF1_L switch", "1"},
	{"ADC_B Right Ip Select ADC_B DIF1_R switch", "1"},
	{"ADC_A MICPGA Volume Ctrl", "40"},
	{"ADC_B MICPGA Volume Ctrl", "40"},
	{"ADC_A Left Mute", "Off"},
	{"ADC_A Right Mute", "Off"},
	{"ADC_B Left Mute", "Off"},
	{"ADC_B Right Mute", "Off"},
	{"HPL Output Mixer L_DAC Switch", "1"},
	{"HPR Output Mixer R_DAC Switch", "1"},
}

// AthenaRoutes are the MT8183 mainline AFE interconnects and the upstream
// TLV320ADC5140 controls verified by TECHO5 on the Echo Show 8 Gen 2. Playback
// is DL1 -> I2S1 -> TAS5805M. Capture is four I2S2 lanes -> UL1 at 96 kHz.
var AthenaRoutes = []Write{
	{"I2S1_CH1 DL1_CH1", "1"},
	{"I2S1_CH2 DL1_CH2", "1"},
	{"Master Playback Volume", "110"},
	{"I2S2_HD_Mux", "Low_Jitter"},
	{"UL1_CH1 I2S2_CH1", "1"},
	{"UL1_CH2 I2S2_CH2", "1"},
	{"UL1_CH3 I2S2_CH3", "1"},
	{"UL1_CH4 I2S2_CH4", "1"},
	{"CH1_ASI_EN Switch", "1"},
	{"CH2_ASI_EN Switch", "1"},
	{"CH3_ASI_EN Switch", "1"},
	{"CH4_ASI_EN Switch", "1"},
	{"Digital CH1 Out Volume", "201"},
	{"Digital CH2 Out Volume", "201"},
	{"Digital CH3 Out Volume", "201"},
	{"Digital CH4 Out Volume", "201"},
	{"Analog CH1 Mic Gain Volume", "20"},
	{"Analog CH2 Mic Gain Volume", "20"},
	{"Analog CH3 Mic Gain Volume", "20"},
	{"Analog CH4 Mic Gain Volume", "20"},
}

var once sync.Once

// EnsureRoutes applies the target's DAPM routes and input gains exactly once
// per process.
//
// Called from both the microphone and the speaker Init, because either may run
// first and each needs the routes closed BEFORE it opens its PCM — DAPM decides
// what to power at stream open.
//
// A control that does not resolve is now a loud failure rather than a write to
// whatever happens to hold that id on this kernel.
func EnsureRoutes(target string) {
	once.Do(func() {
		routes := Routes
		isRadar := strings.EqualFold(strings.TrimSpace(target), "radar")
		total := len(routes)
		if isRadar {
			routes = RadarRoutes
			total = len(routes) + 1 // scalar routes plus the byte-typed EQ
		} else if strings.EqualFold(strings.TrimSpace(target), "checkers") {
			routes = CheckersRoutes
			total = len(routes)
		} else if strings.EqualFold(strings.TrimSpace(target), "cronos") {
			routes = CronosRoutes
			total = len(routes)
		} else if strings.EqualFold(strings.TrimSpace(target), "crown") {
			routes = CrownRoutes
			total = len(routes)
		} else if strings.EqualFold(strings.TrimSpace(target), "rook") {
			routes = RookRoutes
			total = len(routes)
		} else if strings.EqualFold(strings.TrimSpace(target), "athena") {
			routes = AthenaRoutes
			total = len(routes)
		}
		var failed int
		if isRadar {
			// Keep the hardware codec neutral. The shared Tater-native output
			// chain owns EQ, bass protection, and limiting on every Linux target.
			if err := mixer.SetBytes("biquad coefficients", radarFlatEQ); err != nil {
				failed++
			} else {
				log.Printf("[codec] Radar flat speaker filter applied")
			}
		}
		if strings.EqualFold(strings.TrimSpace(target), "cronos") {
			// MAX98396 units boot in a heavily power-limited safe mode. TAS5805M
			// units do not expose this control, so absence is expected.
			_ = mixer.Set("Speaker Safe Mode A", "0")
		}
		for _, w := range routes {
			if err := mixer.Set(w.Name, w.Value); err != nil {
				failed++
			}
		}
		if failed > 0 {
			log.Printf("[codec] %d of %d DAPM routes failed — audio may be silent",
				failed, total)
		} else {
			log.Printf("[codec] %d DAPM routes/tuning controls applied", total)
		}

		inputGains := inputGainsForTarget(target)
		failed = 0
		for _, w := range inputGains {
			if err := mixer.Set(w.Name, w.Value); err != nil {
				failed++
			}
		}
		if failed > 0 {
			log.Printf("[codec] %d of %d input gains failed — microphones may read about 6dB low",
				failed, len(inputGains))
		} else {
			log.Printf("[codec] %d input gains set", len(inputGains))
		}
	})
}
