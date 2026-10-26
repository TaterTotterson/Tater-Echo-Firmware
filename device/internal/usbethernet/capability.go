package usbethernet

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

const qualifiedStatus = "qualified"

// Capability records the hardware boundary that must be satisfied before a
// target may advertise USB Ethernet. Backend is a candidate implementation
// until Status is qualified; merely sharing a controller family never enables
// a target.
type Capability struct {
	Target   string `json:"-"`
	Backend  string `json:"backend"`
	Status   string `json:"status"`
	Port     string `json:"port"`
	Recovery string `json:"recovery"`
	Reason   string `json:"reason"`
}

type capabilityFile struct {
	Schema  int                   `json:"schema"`
	Targets map[string]Capability `json:"targets"`
}

//go:embed capabilities.json
var capabilityJSON []byte

var capabilityCatalog = mustLoadCapabilities()

func mustLoadCapabilities() map[string]Capability {
	var file capabilityFile
	if err := json.Unmarshal(capabilityJSON, &file); err != nil {
		panic(fmt.Sprintf("parse USB Ethernet capabilities: %v", err))
	}
	if file.Schema != 1 {
		panic(fmt.Sprintf("unsupported USB Ethernet capability schema %d", file.Schema))
	}
	for target, capability := range file.Targets {
		capability.Target = target
		file.Targets[target] = capability
	}
	return file.Targets
}

func normalizeTarget(target string) string {
	target = strings.ToLower(strings.TrimSpace(target))
	return strings.TrimPrefix(target, "tater-")
}

// CapabilityForTarget returns an explicit record even for unknown targets, so
// callers fail closed and diagnostics explain why support was withheld.
func CapabilityForTarget(target string) Capability {
	target = normalizeTarget(target)
	if capability, ok := capabilityCatalog[target]; ok {
		return capability
	}
	return Capability{
		Target:  target,
		Backend: "none",
		Status:  "unknown",
		Reason:  "target is not present in the USB Ethernet capability catalog",
	}
}

// Capabilities returns a copy for validation and tooling.
func Capabilities() map[string]Capability {
	result := make(map[string]Capability, len(capabilityCatalog))
	for target, capability := range capabilityCatalog {
		result[target] = capability
	}
	return result
}

func (capability Capability) qualified() bool {
	return capability.Status == qualifiedStatus
}
