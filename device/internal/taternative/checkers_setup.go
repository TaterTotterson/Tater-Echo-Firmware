package taternative

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	DefaultCheckersAPInterface  = "ap0"
	DefaultCheckersAPAddress    = "192.168.4.1"
	DefaultCheckersAPSupplicant = "/data/local/lib/tater/wpa_supplicant-ap"
	defaultCheckersAPConfig     = "/data/local/tmp/tater-setup-ap.conf"
	defaultCheckersRadioDevice  = "/dev/wmtWifi"
	// Linux SO_BINDTODEVICE. Kept local because host-side unit tests also
	// compile this packet code on Darwin, where syscall omits the symbol.
	soBindToDevice = 25
)

// CheckersSetupNetwork owns the temporary, local-only network used by the
// first-boot portal. Fire OS 6's WifiManager starts hostapd on wlan0 and fails
// on this MediaTek board; the hardware AP is the dedicated ap0 interface,
// enabled through /dev/wmtWifi just like the proven emOS Biscuit path.
type CheckersSetupNetwork struct {
	Interface      string
	Address        net.IP
	SupplicantPath string
	ConfigPath     string
	RadioPath      string

	mu         sync.Mutex
	cancel     context.CancelFunc
	supplicant *exec.Cmd
	stopped    bool
}

func (n *CheckersSetupNetwork) defaults() {
	if strings.TrimSpace(n.Interface) == "" {
		n.Interface = DefaultCheckersAPInterface
	}
	if n.Address == nil {
		n.Address = net.ParseIP(DefaultCheckersAPAddress).To4()
	}
	if strings.TrimSpace(n.SupplicantPath) == "" {
		n.SupplicantPath = DefaultCheckersAPSupplicant
	}
	if strings.TrimSpace(n.ConfigPath) == "" {
		n.ConfigPath = defaultCheckersAPConfig
	}
	if strings.TrimSpace(n.RadioPath) == "" {
		n.RadioPath = defaultCheckersRadioDevice
	}
}

func validateSetupSSID(ssid string) error {
	ssid = strings.TrimSpace(ssid)
	if ssid == "" || len(ssid) > 32 {
		return errors.New("setup SSID must contain 1-32 bytes")
	}
	for _, r := range ssid {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return errors.New("setup SSID contains unsupported characters")
		}
	}
	return nil
}

func (n *CheckersSetupNetwork) Start(parent context.Context, ssid string) error {
	n.defaults()
	if err := validateSetupSSID(ssid); err != nil {
		return err
	}
	if n.Address == nil || n.Address.To4() == nil {
		return errors.New("setup address must be IPv4")
	}
	if _, err := os.Stat(n.SupplicantPath); err != nil {
		return fmt.Errorf("setup AP supplicant: %w", err)
	}

	n.mu.Lock()
	if n.cancel != nil {
		n.mu.Unlock()
		return errors.New("setup network already running")
	}
	ctx, cancel := context.WithCancel(parent)
	n.cancel = cancel
	n.stopped = false
	n.mu.Unlock()

	fail := func(err error) error {
		n.Stop()
		return err
	}
	// Checkers' Fire OS 6 kernel does not implement Biscuit's /dev/wmtWifi
	// "A" shortcut. Its framework does know how to retire the station
	// supplicant and enter SoftApState, but then launches stock hostapd on the
	// wrong interface (wlan0). Ask the framework to perform only that lifecycle
	// transition; Tater starts its AP supplicant on the correct ap0 below.
	if out, err := exec.Command("/system/bin/iwpriv", "wlan0", "set_p2p_mode", "1", "1").CombinedOutput(); err != nil {
		return fail(fmt.Errorf("prepare Checkers AP mode: %v (%s)", err, strings.TrimSpace(string(out))))
	}
	// Tater Show is the sole HOME activity after the Amazon launcher is
	// quarantined. Force-stopping it makes Android immediately relaunch HOME
	// without these extras, racing this explicit singleTask launch. Deliver the
	// setup request directly; MainActivity handles it in onNewIntent.
	if out, err := exec.Command("/system/bin/am", checkersSetupActivityArgs(ssid)...).CombinedOutput(); err != nil {
		return fail(fmt.Errorf("start setup screen/hotspot: %v (%s)", err, strings.TrimSpace(string(out))))
	}
	if err := waitForProcessGone(ctx, "wpa_supplicant", 5*time.Second); err != nil {
		return fail(err)
	}
	// The old ap0 sysfs entry survives briefly after the station process exits.
	// Starting against it races the framework's driver reload and loses the
	// interface underneath nl80211. Require the full remove/add edge.
	if err := waitForInterfaceState(ctx, n.Interface, false, 5*time.Second); err != nil {
		return fail(fmt.Errorf("retire old setup interface: %w", err))
	}
	if err := waitForInterfaceState(ctx, n.Interface, true, 5*time.Second); err != nil {
		return fail(err)
	}
	time.Sleep(600 * time.Millisecond)
	_ = exec.Command("/system/bin/ip", "link", "set", n.Interface, "up").Run()

	config := fmt.Sprintf("ctrl_interface=/data/local/tmp/tater-setup-sockets\nap_scan=2\nnetwork={\n\tssid=\"%s\"\n\tmode=2\n\tfrequency=2437\n\tkey_mgmt=NONE\n}\n", ssid)
	if err := os.MkdirAll("/data/local/tmp/tater-setup-sockets", 0o700); err != nil {
		return fail(fmt.Errorf("create AP control directory: %w", err))
	}
	if err := os.WriteFile(n.ConfigPath, []byte(config), 0o600); err != nil {
		return fail(fmt.Errorf("write AP config: %w", err))
	}
	cmd := exec.CommandContext(ctx, n.SupplicantPath, "-q", "-i"+n.Interface,
		"-Dnl80211", "-c"+n.ConfigPath)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return fail(fmt.Errorf("start AP supplicant: %w", err))
	}
	n.mu.Lock()
	n.supplicant = cmd
	n.mu.Unlock()
	go func() {
		if err := cmd.Wait(); err != nil && ctx.Err() == nil {
			log.Printf("[setup] AP supplicant stopped: %v", err)
		}
	}()
	// WifiStateMachine performs one final interface cleanup after its stock
	// hostapd has already failed. Assigning our address before that cleanup
	// creates a deceptive AP that still beacons but cannot serve DHCP.
	time.Sleep(2500 * time.Millisecond)

	address := n.Address.To4().String()
	_ = exec.Command("/system/bin/ip", "addr", "flush", "dev", n.Interface).Run()
	if out, err := exec.Command("/system/bin/ip", "addr", "add", address+"/24", "dev", n.Interface).CombinedOutput(); err != nil {
		return fail(fmt.Errorf("address setup interface: %v (%s)", err, strings.TrimSpace(string(out))))
	}
	if out, err := exec.Command("/system/bin/ip", "link", "set", n.Interface, "up").CombinedOutput(); err != nil {
		return fail(fmt.Errorf("bring setup interface up: %v (%s)", err, strings.TrimSpace(string(out))))
	}
	go reconcileSetupAddress(ctx, n.Interface, address)

	if err := allowSetupTraffic(n.Interface); err != nil {
		return fail(err)
	}
	if err := serveSetupDNS(ctx, n.Interface, n.Address); err != nil {
		return fail(err)
	}
	if err := serveSetupDHCP(ctx, n.Interface, n.Address); err != nil {
		return fail(err)
	}
	log.Printf("[setup] network %s active on %s (%s)", ssid, n.Interface, address)
	return nil
}

func checkersSetupActivityArgs(ssid string) []string {
	return []string{"start", "-n", "com.tatertotterson.show/.MainActivity",
		"--ez", "setup", "true", "--ez", "manage_hotspot", "true",
		"--es", "setup_ssid", ssid}
}

func reconcileSetupAddress(ctx context.Context, iface, address string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			out, _ := exec.Command("/system/bin/ip", "addr", "show", "dev", iface).Output()
			if strings.Contains(string(out), "inet "+address+"/24") {
				continue
			}
			log.Printf("[setup] restoring %s/24 on %s after Fire OS cleanup", address, iface)
			_ = exec.Command("/system/bin/ip", "addr", "add", address+"/24", "dev", iface).Run()
			_ = exec.Command("/system/bin/ip", "link", "set", iface, "up").Run()
		}
	}
}

func (n *CheckersSetupNetwork) Stop() {
	n.defaults()
	n.mu.Lock()
	if n.stopped {
		n.mu.Unlock()
		return
	}
	n.stopped = true
	cancel, cmd := n.cancel, n.supplicant
	n.cancel, n.supplicant = nil, nil
	n.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	_ = exec.Command("/system/bin/ip", "link", "set", n.Interface, "down").Run()
	_ = exec.Command("/system/bin/sh", "/system/bin/svc", "wifi", "disable").Run()
	time.Sleep(500 * time.Millisecond)
	_ = writeRadioMode(n.RadioPath, "0")
	time.Sleep(250 * time.Millisecond)
	_ = writeRadioMode(n.RadioPath, "1")
	_ = exec.Command("/system/bin/setprop", "wifi.interface", "wlan0").Run()
	_ = exec.Command("/system/bin/sh", "/system/bin/svc", "wifi", "enable").Run()
}

func writeRadioMode(path, mode string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString(mode)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func waitForInterfaceState(ctx context.Context, name string, present bool, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	path := "/sys/class/net/" + name
	for {
		// Fire OS denies NETLINK_ROUTE dumps to the Magisk-launched daemon
		// even though its bounded ip operations are allowed. sysfs is the
		// kernel's authoritative existence check and is what emOS uses too.
		_, err := os.Stat(path)
		if (err == nil) == present {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			verb := "appear"
			if !present {
				verb = "disappear"
			}
			return fmt.Errorf("setup interface %s did not %s within %s", name, verb, timeout)
		case <-tick.C:
		}
	}
}

func waitForProcessGone(ctx context.Context, name string, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if !processNamed(name) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("process %s did not stop within %s", name, timeout)
		case <-tick.C:
		}
	}
}

func processNamed(name string) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "self" {
			continue
		}
		comm, err := os.ReadFile("/proc/" + entry.Name() + "/comm")
		if err == nil && strings.TrimSpace(string(comm)) == name {
			return true
		}
	}
	return false
}

func allowSetupTraffic(iface string) error {
	for _, rule := range [][]string{
		{"-I", "INPUT", "-i", iface, "-p", "udp", "--dport", "53", "-j", "ACCEPT"},
		{"-I", "INPUT", "-i", iface, "-p", "udp", "--dport", "67", "-j", "ACCEPT"},
		{"-I", "INPUT", "-i", iface, "-p", "tcp", "--dport", "80", "-j", "ACCEPT"},
	} {
		if out, err := exec.Command("/system/bin/iptables", rule...).CombinedOutput(); err != nil {
			return fmt.Errorf("setup firewall: %v (%s)", err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func serveSetupDNS(ctx context.Context, iface string, answer net.IP) error {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 53})
	if err != nil {
		return fmt.Errorf("setup DNS: %w", err)
	}
	if err := bindUDPToInterface(conn, iface, false); err != nil {
		_ = conn.Close()
		return fmt.Errorf("bind setup DNS to %s: %w", iface, err)
	}
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
	go func() {
		buf := make([]byte, 512)
		for {
			n, peer, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if reply := setupDNSReply(buf[:n], answer); reply != nil {
				_, _ = conn.WriteToUDP(reply, peer)
			}
		}
	}()
	return nil
}

func setupDNSReply(query []byte, answer net.IP) []byte {
	if len(query) < 17 || binary.BigEndian.Uint16(query[4:6]) == 0 {
		return nil
	}
	end := 12
	for {
		if end >= len(query) {
			return nil
		}
		length := int(query[end])
		end++
		if length == 0 {
			break
		}
		if length > 63 || end+length > len(query) {
			return nil
		}
		end += length
	}
	if end+4 > len(query) {
		return nil
	}
	end += 4
	reply := append([]byte(nil), query[:end]...)
	binary.BigEndian.PutUint16(reply[2:4], 0x8180)
	binary.BigEndian.PutUint16(reply[4:6], 1)
	binary.BigEndian.PutUint16(reply[6:8], 1)
	binary.BigEndian.PutUint16(reply[8:10], 0)
	binary.BigEndian.PutUint16(reply[10:12], 0)
	reply = append(reply, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 0, 0, 4)
	return append(reply, answer.To4()...)
}

func serveSetupDHCP(ctx context.Context, iface string, server net.IP) error {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 67})
	if err != nil {
		return fmt.Errorf("setup DHCP: %w", err)
	}
	if err := bindUDPToInterface(conn, iface, true); err != nil {
		_ = conn.Close()
		return fmt.Errorf("bind setup DHCP to %s: %w", iface, err)
	}
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
	go func() {
		buf := make([]byte, 1500)
		broadcast := &net.UDPAddr{IP: net.IPv4(192, 168, 4, 255), Port: 68}
		for {
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if reply := setupDHCPReply(buf[:n], server); reply != nil {
				_, _ = conn.WriteToUDP(reply, broadcast)
			}
		}
	}()
	return nil
}

func bindUDPToInterface(conn *net.UDPConn, iface string, broadcast bool) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		if err := syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, soBindToDevice, iface); err != nil {
			socketErr = err
			return
		}
		if broadcast {
			socketErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
		}
	}); err != nil {
		return err
	}
	return socketErr
}

func setupDHCPReply(request []byte, server net.IP) []byte {
	if len(request) < 240 || request[0] != 1 || request[236] != 99 || request[237] != 130 || request[238] != 83 || request[239] != 99 {
		return nil
	}
	messageType := byte(0)
	for pos := 240; pos < len(request); {
		code := request[pos]
		pos++
		if code == 255 {
			break
		}
		if code == 0 {
			continue
		}
		if pos >= len(request) {
			return nil
		}
		length := int(request[pos])
		pos++
		if pos+length > len(request) {
			return nil
		}
		if code == 53 && length == 1 {
			messageType = request[pos]
		}
		pos += length
	}
	responseType := byte(0)
	switch messageType {
	case 1:
		responseType = 2 // DISCOVER -> OFFER
	case 3:
		responseType = 5 // REQUEST -> ACK
	default:
		return nil
	}
	hlen := int(request[2])
	if hlen < 1 || hlen > 16 {
		return nil
	}
	last := byte(2 + crc32.ChecksumIEEE(request[28:28+hlen])%19)
	client := net.IPv4(192, 168, 4, last).To4()
	server = server.To4()
	if server == nil {
		return nil
	}
	reply := make([]byte, 240, 300)
	reply[0], reply[1], reply[2] = 2, request[1], request[2]
	copy(reply[4:8], request[4:8])
	binary.BigEndian.PutUint16(reply[10:12], 0x8000)
	copy(reply[16:20], client)
	copy(reply[20:24], server)
	copy(reply[28:44], request[28:44])
	copy(reply[236:240], []byte{99, 130, 83, 99})
	reply = append(reply,
		53, 1, responseType,
		54, 4, server[0], server[1], server[2], server[3],
		51, 4, 0, 0, 0x0e, 0x10,
		1, 4, 255, 255, 255, 0,
		3, 4, server[0], server[1], server[2], server[3],
		6, 4, server[0], server[1], server[2], server[3],
		255)
	for len(reply) < 300 {
		reply = append(reply, 0)
	}
	return reply
}
