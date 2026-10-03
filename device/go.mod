module github.com/TaterTotterson/Tater-Echo-Firmware

go 1.24.0

require (
	github.com/Binozo/GoTinyAlsa v1.0.3
	github.com/gorilla/websocket v1.5.3
	github.com/grandcat/zeroconf v1.0.0
	github.com/gvalkov/golang-evdev v0.0.0-20220815104727-7e27d6ce89b6
	github.com/hajimehoshi/go-mp3 v0.3.4
	golang.org/x/image v0.32.0
	golang.org/x/sys v0.41.0
)

require (
	github.com/cenkalti/backoff v2.2.1+incompatible // indirect
	github.com/miekg/dns v1.1.41 // indirect
)

// Vendored so the Alpine/Fire OS tinyalsa compatibility fixes are part of
// this firmware generation rather than an unpublishable submodule checkout.
replace github.com/Binozo/GoTinyAlsa => ./third_party/GoTinyAlsa

require (
	golang.org/x/net v0.39.0 // indirect
	golang.org/x/text v0.30.0 // indirect
)
