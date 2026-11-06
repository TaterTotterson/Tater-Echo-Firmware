//go:build linux && !android

package bluetooth

import (
	"errors"
	"fmt"
	"testing"

	"github.com/godbus/dbus/v5"
)

func bluezTestProperties(values map[string]any) map[string]dbus.Variant {
	properties := make(map[string]dbus.Variant, len(values))
	for name, value := range values {
		properties[name] = dbus.MakeVariant(value)
	}
	return properties
}

func TestBlueZPathHandle(t *testing.T) {
	for _, test := range []struct {
		path dbus.ObjectPath
		want uint16
	}{
		{"/org/bluez/hci0/dev_AA/service0001", 0x0001},
		{"/org/bluez/hci0/dev_AA/service0010/char00af", 0x00af},
		{"/org/bluez/hci0/dev_AA/service0010/char0011/descbeef", 0xbeef},
	} {
		got, err := bluezPathHandle(test.path)
		if err != nil || got != test.want {
			t.Fatalf("bluezPathHandle(%q) = 0x%04x, %v; want 0x%04x", test.path, got, err, test.want)
		}
	}
	for _, path := range []dbus.ObjectPath{"/org/bluez/hci0", "/service0000", "/char001", "/charzzzz"} {
		if _, err := bluezPathHandle(path); err == nil {
			t.Fatalf("bluezPathHandle(%q) unexpectedly succeeded", path)
		}
	}
}

func TestBlueZConnectRetryClassification(t *testing.T) {
	transient := fmt.Errorf("wrapped: %w", dbus.Error{Name: "org.bluez.Error.Failed"})
	if !bluezConnectRetryable(transient) {
		t.Fatal("org.bluez.Error.Failed should be retried")
	}
	for _, err := range []error{
		dbus.Error{Name: "org.bluez.Error.NotSupported"},
		dbus.Error{Name: "org.bluez.Error.NotAuthorized"},
		errors.New("local failure"),
	} {
		if bluezConnectRetryable(err) {
			t.Fatalf("%v should not be retried", err)
		}
	}
}

func TestBlueZDiscoveryFilterFallbackIsNarrow(t *testing.T) {
	unsupported := fmt.Errorf("wrapped: %w", dbus.Error{Name: "org.bluez.Error.NotSupported"})
	if !bluezDiscoveryFilterUnsupported(unsupported) {
		t.Fatal("org.bluez.Error.NotSupported should use unfiltered discovery")
	}
	if !bluezDiscoveryFilterUnsupported(dbus.NewError("org.bluez.Error.NotSupported", nil)) {
		t.Fatal("a pointer D-Bus NotSupported error should use unfiltered discovery")
	}
	for _, err := range []error{
		dbus.Error{Name: "org.bluez.Error.Failed"},
		dbus.Error{Name: "org.bluez.Error.NotReady"},
		errors.New("local failure"),
	} {
		if bluezDiscoveryFilterUnsupported(err) {
			t.Fatalf("%v should not be hidden by the discovery fallback", err)
		}
	}
}

func TestBlueZBuildServicesUsesATTHandles(t *testing.T) {
	device := dbus.ObjectPath("/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF")
	service1 := device + "/service0010"
	char1 := service1 + "/char0012"
	desc1 := char1 + "/desc0014"
	char2 := service1 + "/char0016"
	desc2 := char2 + "/desc0018"
	service2 := device + "/service0020"
	objects := bluezManagedObjects{
		service1: {bluezService: bluezTestProperties(map[string]any{
			"Device": device, "Primary": true, "UUID": "0000180f-0000-1000-8000-00805f9b34fb",
		})},
		char1: {bluezCharacteristic: bluezTestProperties(map[string]any{
			"Service": service1, "UUID": "00002a19-0000-1000-8000-00805f9b34fb",
			"Flags": []string{"read", "notify"}, "MTU": uint16(185),
		})},
		desc1: {bluezDescriptor: bluezTestProperties(map[string]any{
			"Characteristic": char1, "UUID": "00002901-0000-1000-8000-00805f9b34fb",
		})},
		char2: {bluezCharacteristic: bluezTestProperties(map[string]any{
			"Service": service1, "UUID": "00002a1a-0000-1000-8000-00805f9b34fb",
			"Flags": []string{"indicate"},
		})},
		desc2: {bluezDescriptor: bluezTestProperties(map[string]any{
			"Characteristic": char2, "UUID": "00002902-0000-1000-8000-00805f9b34fb",
		})},
		service2: {bluezService: bluezTestProperties(map[string]any{
			"Device": device, "Primary": true, "UUID": "00001801-0000-1000-8000-00805f9b34fb",
		})},
	}

	services, attributes, handles, err := bluezBuildServices(objects, device)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 2 || services[0].Start != 0x0010 || services[0].End != 0x001f {
		t.Fatalf("services = %#v; want first service range 0x0010-0x001f", services)
	}
	chars := services[0].Characteristics
	if len(chars) != 2 || chars[0].Handle != 0x0012 || chars[0].ValueHandle != 0x0013 || chars[1].Handle != 0x0016 || chars[1].ValueHandle != 0x0017 {
		t.Fatalf("characteristics = %#v; handles were not preserved", chars)
	}
	if len(chars[0].Descriptors) != 2 || chars[0].Descriptors[0].Handle != 0x0014 || chars[0].Descriptors[1].Handle != 0x0015 || !chars[0].Descriptors[1].UUID.Is16(gattCCCD) {
		t.Fatalf("first characteristic descriptors = %#v; want real descriptor and inferred CCCD handles", chars[0].Descriptors)
	}
	if len(chars[1].Descriptors) != 1 || chars[1].Descriptors[0].Handle != 0x0018 || attributes[0x0018].kind != "cccd" {
		t.Fatalf("second characteristic descriptors = %#v; exposed CCCD was not preserved", chars[1].Descriptors)
	}
	if handles[char1] != 0x0013 || attributes[0x0013].path != char1 || attributes[0x0015].charPath != char1 {
		t.Fatalf("handle indexes = %#v, %#v", handles, attributes)
	}
	manager := &BlueZManager{}
	link := &bluezLink{handleByPath: handles}
	if mtu := manager.connectionMTU(t.Context(), objects, link); mtu != 185 {
		t.Fatalf("connectionMTU = %d; want 185", mtu)
	}
}

func TestBlueZValueSignalRequiresSignalSubscription(t *testing.T) {
	const address = "aa:bb:cc:dd:ee:ff"
	path := dbus.ObjectPath("/org/bluez/hci0/dev_AA/service0001/char0002")
	link := &bluezLink{
		address:       address,
		device:        "/org/bluez/hci0/dev_AA",
		attributes:    map[uint16]bluezAttribute{3: {kind: "characteristic", path: path, charPath: path}},
		handleByPath:  map[dbus.ObjectPath]uint16{path: 3},
		notifications: map[uint16]*bluezNotification{},
	}
	manager := &BlueZManager{links: map[string]*bluezLink{address: link}}
	var calls int
	manager.onNotify = func(gotAddress string, handle uint16, value []byte, indication bool) {
		calls++
		if gotAddress != address || handle != 3 || len(value) != 1 || value[0] != 7 || indication {
			t.Errorf("unexpected notification: %q 0x%04x %v %v", gotAddress, handle, value, indication)
		}
	}
	signal := &dbus.Signal{
		Name: bluezProperties + ".PropertiesChanged", Path: path,
		Body: []any{bluezCharacteristic, map[string]dbus.Variant{"Value": dbus.MakeVariant([]byte{7})}},
	}
	manager.handleSignal(signal)
	if calls != 0 {
		t.Fatal("unsubscribed Value property update was forwarded as a notification")
	}
	link.notifications[3] = &bluezNotification{viaSignal: true}
	manager.handleSignal(signal)
	if calls != 1 {
		t.Fatalf("signal-backed subscription received %d callbacks; want 1", calls)
	}
	link.notifications[3] = &bluezNotification{viaSignal: false}
	manager.handleSignal(signal)
	if calls != 1 {
		t.Fatal("AcquireNotify-backed subscription also forwarded the Value property signal")
	}
}

func TestBlueZRemovalSignalsCleanUpLinks(t *testing.T) {
	const address = "aa:bb:cc:dd:ee:ff"
	device := dbus.ObjectPath("/org/bluez/hci0/dev_AA")
	stopped := false
	unsubscribed := true
	link := &bluezLink{
		address: address, device: device,
		notifications: map[uint16]*bluezNotification{3: {stop: func(unsubscribe bool) {
			stopped, unsubscribed = true, unsubscribe
		}}},
	}
	manager := &BlueZManager{adapter: "/org/bluez/hci0", links: map[string]*bluezLink{address: link}}
	var disconnected string
	manager.onDisconnect = func(got string, reason byte) {
		disconnected = got
		if reason != bluezDisconnect {
			t.Errorf("disconnect reason = 0x%02x; want 0x%02x", reason, bluezDisconnect)
		}
	}
	manager.handleSignal(&dbus.Signal{
		Name: bluezObjectManager + ".InterfacesRemoved",
		Body: []any{device, []string{bluezDevice}},
	})
	if len(manager.links) != 0 || disconnected != address || !stopped || unsubscribed {
		t.Fatalf("cleanup: links=%d disconnected=%q stopped=%v unsubscribed=%v", len(manager.links), disconnected, stopped, unsubscribed)
	}

	manager.links[address] = &bluezLink{address: address, device: device, notifications: map[uint16]*bluezNotification{}}
	manager.handleSignal(&dbus.Signal{
		Name: "org.freedesktop.DBus.NameOwnerChanged",
		Body: []any{bluezBus, ":1.5", ""},
	})
	if len(manager.links) != 0 {
		t.Fatal("BlueZ owner loss did not remove active links")
	}
}
