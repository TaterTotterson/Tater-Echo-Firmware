package taternative

import (
	"encoding/binary"
	"net"
	"reflect"
	"testing"
)

func TestSetupDNSReplyPointsAtPortal(t *testing.T) {
	query := []byte{0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0,
		7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1}
	reply := setupDNSReply(query, net.IPv4(192, 168, 4, 1))
	if len(reply) < len(query)+16 || binary.BigEndian.Uint16(reply[6:8]) != 1 {
		t.Fatalf("bad DNS reply: %x", reply)
	}
	if got := net.IP(reply[len(reply)-4:]).String(); got != "192.168.4.1" {
		t.Fatalf("answer = %s", got)
	}
}

func TestSetupDHCPDiscoverAndRequest(t *testing.T) {
	packet := make([]byte, 244)
	packet[0], packet[1], packet[2] = 1, 1, 6
	copy(packet[4:8], []byte{1, 2, 3, 4})
	copy(packet[28:34], []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff})
	copy(packet[236:240], []byte{99, 130, 83, 99})
	copy(packet[240:], []byte{53, 1, 1, 255})
	offer := setupDHCPReply(packet, net.IPv4(192, 168, 4, 1))
	if len(offer) < 300 || offer[0] != 2 || offer[242] != 2 || binary.BigEndian.Uint16(offer[10:12]) != 0x8000 {
		t.Fatalf("bad DHCP offer: %x", offer)
	}
	packet[242] = 3
	ack := setupDHCPReply(packet, net.IPv4(192, 168, 4, 1))
	if len(ack) < 250 || ack[242] != 5 {
		t.Fatalf("bad DHCP ack: %x", ack)
	}
}

func TestValidateSetupSSID(t *testing.T) {
	for _, value := range []string{"", "bad\nname", `bad"name`, string(make([]byte, 33))} {
		if validateSetupSSID(value) == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	if err := validateSetupSSID("Tater-Setup-06X3"); err != nil {
		t.Fatal(err)
	}
}

func TestCheckersSetupLaunchDeliversHotspotIntentWithoutForceStop(t *testing.T) {
	want := []string{"start", "-n", "com.tatertotterson.show/.MainActivity",
		"--ez", "setup", "true", "--ez", "manage_hotspot", "true",
		"--es", "setup_ssid", "Tater-Setup-06X3"}
	if got := checkersSetupActivityArgs("Tater-Setup-06X3"); !reflect.DeepEqual(got, want) {
		t.Fatalf("setup activity args = %q, want %q", got, want)
	}
}
