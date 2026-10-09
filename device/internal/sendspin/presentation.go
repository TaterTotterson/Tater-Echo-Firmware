package sendspin

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"strings"
	"time"
)

// NowPlaying is the presentation state attached to the admitted Sendspin
// session. Artwork is copied only when callers ask for a snapshot, so the
// high-rate visualizer stream never retransmits or reallocates it.
type NowPlaying struct {
	InfoRevision       uint64
	VisualizerRevision uint64
	Active             bool
	PlaybackState      string
	GroupName          string
	Title              string
	Artist             string
	AlbumArtist        string
	Album              string
	ArtworkURL         string
	PrimaryColor       [3]uint8
	AccentColor        [3]uint8
	ProgressMS         int64
	DurationMS         int64
	PlaybackSpeed      int
	HasLoudness        bool
	Loudness           float64
	Peak               float64
	PeakSequence       uint64
	Spectrum           []float64
	BeatSequence       uint64
}

type presentationState struct {
	NowPlaying
	artwork            []byte
	artworkContentType string
	visualQueue        []queuedVisualizer
	progressUpdatedAt  time.Time
	visualReceived     uint64
	visualApplied      uint64
	spectrumReceived   uint64
	spectrumApplied    uint64
	lastVisualReceived time.Time
	lastVisualApplied  time.Time
}

type queuedVisualizer struct {
	at          int64
	messageType byte
	payload     []byte
	bins        int
}

func (c *Client) NowPlaying() NowPlaying {
	c.mu.Lock()
	defer c.mu.Unlock()
	nowClient := nowUs()
	c.advanceVisualizerLocked(nowClient)
	now := c.presentation.NowPlaying
	if now.PlaybackSpeed > 0 && !c.presentation.progressUpdatedAt.IsZero() {
		now.ProgressMS += max(int64(0), time.Since(c.presentation.progressUpdatedAt).Microseconds()) * int64(now.PlaybackSpeed) / 1_000_000
		if now.DurationMS > 0 {
			now.ProgressMS = min(now.ProgressMS, now.DurationMS)
		}
	}
	now.Spectrum = append([]float64(nil), c.presentation.Spectrum...)
	return now
}

// NowPlayingArtwork copies the low-rate artwork payload separately from the
// 10 Hz presentation poll. This avoids repeatedly allocating a cover image
// merely because loudness or spectrum frames arrived.
func (c *Client) NowPlayingArtwork() ([]byte, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.presentation.artwork...), c.presentation.artworkContentType
}

func (c *Client) queueVisualizer(messageType byte, payload []byte, bins int, at int64) {
	if len(payload) < 8 {
		return
	}
	c.mu.Lock()
	if len(c.presentation.visualQueue) >= 2048 {
		copy(c.presentation.visualQueue, c.presentation.visualQueue[1:])
		c.presentation.visualQueue = c.presentation.visualQueue[:len(c.presentation.visualQueue)-1]
	}
	c.presentation.visualQueue = append(c.presentation.visualQueue, queuedVisualizer{
		at: at, messageType: messageType, payload: append([]byte(nil), payload...), bins: bins,
	})
	c.presentation.visualReceived++
	if messageType == msgVisualizerSpectrum {
		c.presentation.spectrumReceived++
	}
	c.presentation.lastVisualReceived = time.Now()
	c.mu.Unlock()
}

func (c *Client) advanceVisualizerLocked(now int64) {
	count := 0
	for count < len(c.presentation.visualQueue) && c.presentation.visualQueue[count].at <= now {
		frame := c.presentation.visualQueue[count]
		if c.applyVisualizerLocked(frame.messageType, frame.payload, frame.bins) {
			c.presentation.visualApplied++
			if frame.messageType == msgVisualizerSpectrum {
				c.presentation.spectrumApplied++
			}
			c.presentation.lastVisualApplied = time.Now()
		}
		count++
	}
	if count > 0 {
		copy(c.presentation.visualQueue, c.presentation.visualQueue[count:])
		c.presentation.visualQueue = c.presentation.visualQueue[:len(c.presentation.visualQueue)-count]
	}
}

func (c *Client) clearPresentation() {
	c.mu.Lock()
	info := c.presentation.InfoRevision + 1
	visual := c.presentation.VisualizerRevision + 1
	c.presentation = presentationState{NowPlaying: NowPlaying{
		InfoRevision: info, VisualizerRevision: visual,
	}}
	c.mu.Unlock()
}

func (c *Client) updateGroupPresentation(group groupUpdate) {
	c.mu.Lock()
	changed := c.presentation.GroupName != group.GroupName ||
		c.presentation.PlaybackState != group.PlaybackState
	c.presentation.GroupName = group.GroupName
	c.presentation.PlaybackState = strings.ToLower(strings.TrimSpace(group.PlaybackState))
	c.presentation.Active = c.presentation.PlaybackState == "playing" || c.presentation.PlaybackState == "paused"
	if changed {
		c.presentation.InfoRevision++
	}
	c.mu.Unlock()
}

func (c *Client) applyMetadata(raw json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if isJSONNull(raw) {
		c.presentation.Title = ""
		c.presentation.Artist = ""
		c.presentation.AlbumArtist = ""
		c.presentation.Album = ""
		c.presentation.ArtworkURL = ""
		c.presentation.artwork = nil
		c.presentation.artworkContentType = ""
		c.presentation.ProgressMS = 0
		c.presentation.DurationMS = 0
		c.presentation.PlaybackSpeed = 0
		c.presentation.progressUpdatedAt = time.Time{}
		c.presentation.InfoRevision++
		return
	}
	var fields map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &fields) != nil {
		return
	}
	setString := func(name string, target *string) {
		value, ok := fields[name]
		if !ok {
			return
		}
		if isJSONNull(value) {
			*target = ""
			return
		}
		_ = json.Unmarshal(value, target)
	}
	setString("title", &c.presentation.Title)
	setString("artist", &c.presentation.Artist)
	setString("album_artist", &c.presentation.AlbumArtist)
	setString("album", &c.presentation.Album)
	if _, ok := fields["artwork_url"]; ok {
		setString("artwork_url", &c.presentation.ArtworkURL)
	}
	if progress, ok := fields["progress"]; ok {
		if isJSONNull(progress) {
			c.presentation.ProgressMS = 0
			c.presentation.DurationMS = 0
			c.presentation.PlaybackSpeed = 0
			c.presentation.progressUpdatedAt = time.Time{}
		} else {
			var value struct {
				Progress int64 `json:"track_progress"`
				Duration int64 `json:"track_duration"`
				Speed    int   `json:"playback_speed"`
			}
			if json.Unmarshal(progress, &value) == nil {
				c.presentation.ProgressMS = max(int64(0), value.Progress)
				c.presentation.DurationMS = max(int64(0), value.Duration)
				c.presentation.PlaybackSpeed = max(0, value.Speed)
				c.presentation.progressUpdatedAt = time.Now()
			}
		}
	}
	c.presentation.InfoRevision++
}

func (c *Client) applyColor(raw json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if isJSONNull(raw) {
		c.presentation.PrimaryColor = [3]uint8{}
		c.presentation.AccentColor = [3]uint8{}
		c.presentation.InfoRevision++
		return
	}
	var fields map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &fields) != nil {
		return
	}
	for name, target := range map[string]*[3]uint8{
		"primary": &c.presentation.PrimaryColor,
		"accent":  &c.presentation.AccentColor,
	} {
		value, ok := fields[name]
		if !ok {
			continue
		}
		if isJSONNull(value) {
			*target = [3]uint8{}
			continue
		}
		var channels []int
		if json.Unmarshal(value, &channels) == nil && len(channels) == 3 {
			for index := range 3 {
				(*target)[index] = uint8(max(0, min(255, channels[index])))
			}
		}
	}
	c.presentation.InfoRevision++
}

func (c *Client) applyArtwork(contents []byte, contentType string) {
	if len(contents) == 0 || len(contents) > maxArtworkBytes {
		return
	}
	c.mu.Lock()
	c.presentation.artwork = append(c.presentation.artwork[:0], contents...)
	c.presentation.artworkContentType = contentType
	c.presentation.InfoRevision++
	c.mu.Unlock()
}

func (c *Client) clearArtwork() {
	c.mu.Lock()
	if len(c.presentation.artwork) > 0 || c.presentation.artworkContentType != "" {
		c.presentation.artwork = nil
		c.presentation.artworkContentType = ""
		c.presentation.InfoRevision++
	}
	c.mu.Unlock()
}

func (c *Client) applyVisualizerLocked(messageType byte, payload []byte, bins int) bool {
	if len(payload) < 8 {
		return false
	}
	data := payload[8:]
	switch messageType {
	case msgVisualizerLoudness:
		if len(data) != 2 {
			return false
		}
		c.presentation.Loudness = float64(binary.BigEndian.Uint16(data)) / math.MaxUint16
		c.presentation.HasLoudness = true
	case msgVisualizerBeat:
		if len(data) != 1 {
			return false
		}
		c.presentation.BeatSequence++
	case msgVisualizerSpectrum:
		if bins <= 0 || len(data) != bins*2 {
			return false
		}
		c.presentation.Spectrum = make([]float64, bins)
		for index := range bins {
			c.presentation.Spectrum[index] = float64(binary.BigEndian.Uint16(data[index*2:])) / math.MaxUint16
		}
	case msgVisualizerPeak:
		if len(data) != 1 {
			return false
		}
		c.presentation.Peak = float64(data[0]) / math.MaxUint8
		c.presentation.PeakSequence++
	default:
		return false
	}
	c.presentation.VisualizerRevision++
	return true
}

func isJSONNull(raw json.RawMessage) bool {
	return len(raw) > 0 && bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
