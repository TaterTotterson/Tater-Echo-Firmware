//go:build linux && !android

package bluetooth

// Rook's Linux kernel and vendor UART bridge expose the controller as hci0,
// which bluetoothd owns. Active GATT must therefore use BlueZ's D-Bus API;
// issuing LE Create Connection or ATT packets on the raw monitor socket would
// race bluetoothd's connection and security state.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	bluezBus             = "org.bluez"
	bluezObjectManager   = "org.freedesktop.DBus.ObjectManager"
	bluezProperties      = "org.freedesktop.DBus.Properties"
	bluezAdapter         = "org.bluez.Adapter1"
	bluezDevice          = "org.bluez.Device1"
	bluezAgent           = "org.bluez.Agent1"
	bluezAgentManager    = "org.bluez.AgentManager1"
	bluezService         = "org.bluez.GattService1"
	bluezCharacteristic  = "org.bluez.GattCharacteristic1"
	bluezDescriptor      = "org.bluez.GattDescriptor1"
	bluezConnectTimeout  = 25 * time.Second
	bluezDiscoverTimeout = 20 * time.Second
	bluezCallTimeout     = 15 * time.Second
	bluezDisconnect      = 0x13
)

type bluezManagedObjects map[dbus.ObjectPath]map[string]map[string]dbus.Variant

type bluezAttribute struct {
	kind       string
	path       dbus.ObjectPath
	charPath   dbus.ObjectPath
	indication bool
}

type bluezLink struct {
	address      string
	device       dbus.ObjectPath
	services     []Service
	attributes   map[uint16]bluezAttribute
	handleByPath map[dbus.ObjectPath]uint16
}

type bluezPairing struct {
	device dbus.ObjectPath
	pin    string
	used   bool
}

// BlueZManager implements GATTManager without taking ownership of hci0.
type BlueZManager struct {
	bus     *dbus.Conn
	adapter dbus.ObjectPath
	hold    func(bool)

	mu           sync.Mutex
	links        map[string]*bluezLink
	onNotify     func(string, uint16, []byte, bool)
	onDisconnect func(string, byte)

	pairMu sync.Mutex
	pair   bluezPairing
}

type bluezPairingAgent struct{ manager *BlueZManager }

// NewBlueZGATTManager attaches to the system bluetoothd and registers a
// KeyboardOnly agent. The existing TECHO5 boot path keeps BlueZ's key store on
// /data, so bonds survive both reboot and application-slot OTA.
func NewBlueZGATTManager(hold func(bool)) (GATTManager, error) {
	bus, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("ble: connect system D-Bus: %w", err)
	}
	fail := func(err error) (GATTManager, error) {
		_ = bus.Close()
		return nil, err
	}
	var owner bool
	if call := bus.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, bluezBus); call.Err != nil {
		return fail(fmt.Errorf("ble: query BlueZ: %w", call.Err))
	} else if err := call.Store(&owner); err != nil || !owner {
		if err != nil {
			return fail(fmt.Errorf("ble: query BlueZ owner: %w", err))
		}
		return fail(errors.New("ble: bluetoothd is not running"))
	}
	m := &BlueZManager{bus: bus, hold: hold, links: map[string]*bluezLink{}}
	objects, err := m.managedObjects(context.Background())
	if err != nil {
		return fail(err)
	}
	for path, interfaces := range objects {
		if _, ok := interfaces[bluezAdapter]; ok && (m.adapter == "" || strings.HasSuffix(string(path), "/hci0")) {
			m.adapter = path
		}
	}
	if m.adapter == "" {
		return fail(errors.New("ble: BlueZ has no adapter"))
	}

	agentPath := dbus.ObjectPath("/com/tater/ble/agent/p" + strconv.Itoa(os.Getpid()))
	if err := bus.Export(&bluezPairingAgent{manager: m}, agentPath, bluezAgent); err != nil {
		return fail(fmt.Errorf("ble: export BlueZ pairing agent: %w", err))
	}
	agentManager := bus.Object(bluezBus, "/org/bluez")
	if call := agentManager.Call(bluezAgentManager+".RegisterAgent", 0, agentPath, "KeyboardOnly"); call.Err != nil {
		return fail(fmt.Errorf("ble: register BlueZ pairing agent: %w", call.Err))
	}
	if call := agentManager.Call(bluezAgentManager+".RequestDefaultAgent", 0, agentPath); call.Err != nil {
		return fail(fmt.Errorf("ble: select BlueZ pairing agent: %w", call.Err))
	}
	if err := bus.AddMatchSignal(
		dbus.WithMatchSender(bluezBus),
		dbus.WithMatchInterface(bluezProperties),
		dbus.WithMatchMember("PropertiesChanged"),
	); err != nil {
		return fail(fmt.Errorf("ble: subscribe to BlueZ properties: %w", err))
	}
	signals := make(chan *dbus.Signal, 128)
	bus.Signal(signals)
	go m.signalLoop(signals)
	return m, nil
}

func (m *BlueZManager) SetCallbacks(onNotify func(string, uint16, []byte, bool), onDisconnect func(string, byte)) {
	m.mu.Lock()
	m.onNotify, m.onDisconnect = onNotify, onDisconnect
	m.mu.Unlock()
}

func (m *BlueZManager) managedObjects(ctx context.Context) (bluezManagedObjects, error) {
	var objects bluezManagedObjects
	call := m.bus.Object(bluezBus, "/").CallWithContext(ctx, bluezObjectManager+".GetManagedObjects", 0)
	if call.Err != nil {
		return nil, fmt.Errorf("ble: read BlueZ objects: %w", call.Err)
	}
	if err := call.Store(&objects); err != nil {
		return nil, fmt.Errorf("ble: decode BlueZ objects: %w", err)
	}
	return objects, nil
}

func bluezVariantString(properties map[string]dbus.Variant, name string) string {
	if value, ok := properties[name]; ok {
		if text, ok := value.Value().(string); ok {
			return text
		}
	}
	return ""
}

func bluezVariantPath(properties map[string]dbus.Variant, name string) dbus.ObjectPath {
	if value, ok := properties[name]; ok {
		if path, ok := value.Value().(dbus.ObjectPath); ok {
			return path
		}
	}
	return ""
}

func bluezVariantBool(properties map[string]dbus.Variant, name string) bool {
	if value, ok := properties[name]; ok {
		if flag, ok := value.Value().(bool); ok {
			return flag
		}
	}
	return false
}

func bluezVariantStrings(properties map[string]dbus.Variant, name string) []string {
	if value, ok := properties[name]; ok {
		if rows, ok := value.Value().([]string); ok {
			return rows
		}
	}
	return nil
}

func bluezErrorNamed(err error, suffixes ...string) bool {
	var dbusErr dbus.Error
	if !errors.As(err, &dbusErr) {
		return false
	}
	for _, suffix := range suffixes {
		if strings.HasSuffix(dbusErr.Name, suffix) {
			return true
		}
	}
	return false
}

func (m *BlueZManager) deviceByAddress(objects bluezManagedObjects, addr string) (dbus.ObjectPath, bool) {
	for path, interfaces := range objects {
		properties, ok := interfaces[bluezDevice]
		if ok && strings.EqualFold(bluezVariantString(properties, "Address"), addr) {
			return path, true
		}
	}
	return "", false
}

func (m *BlueZManager) discoverDevice(ctx context.Context, addr string) (dbus.ObjectPath, error) {
	if objects, err := m.managedObjects(ctx); err == nil {
		if path, ok := m.deviceByAddress(objects, addr); ok {
			return path, nil
		}
	}
	adapter := m.bus.Object(bluezBus, m.adapter)
	filter := map[string]dbus.Variant{
		"Transport":     dbus.MakeVariant("le"),
		"DuplicateData": dbus.MakeVariant(false),
	}
	if call := adapter.CallWithContext(ctx, bluezAdapter+".SetDiscoveryFilter", 0, filter); call.Err != nil {
		return "", fmt.Errorf("ble: set BlueZ discovery filter: %w", call.Err)
	}
	started := false
	if call := adapter.CallWithContext(ctx, bluezAdapter+".StartDiscovery", 0); call.Err == nil {
		started = true
	} else if !bluezErrorNamed(call.Err, ".InProgress") {
		return "", fmt.Errorf("ble: start BlueZ discovery: %w", call.Err)
	}
	if started {
		defer adapter.Call(bluezAdapter+".StopDiscovery", 0)
	}
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		objects, err := m.managedObjects(ctx)
		if err == nil {
			if path, ok := m.deviceByAddress(objects, addr); ok {
				return path, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ErrConnectTimeout
		case <-ticker.C:
		}
	}
}

func (m *BlueZManager) property(ctx context.Context, path dbus.ObjectPath, iface, name string) (dbus.Variant, error) {
	var value dbus.Variant
	call := m.bus.Object(bluezBus, path).CallWithContext(ctx, bluezProperties+".Get", 0, iface, name)
	if call.Err != nil {
		return value, call.Err
	}
	return value, call.Store(&value)
}

func (m *BlueZManager) boolProperty(ctx context.Context, path dbus.ObjectPath, iface, name string) bool {
	value, err := m.property(ctx, path, iface, name)
	if err != nil {
		return false
	}
	flag, _ := value.Value().(bool)
	return flag
}

func (m *BlueZManager) Connect(addr string, addrType int) (ConnInfo, error) {
	addr, ok := normaliseAddr(addr)
	if !ok || addrType < 0 || addrType > 1 {
		return ConnInfo{}, fmt.Errorf("ble: bad address %q type %d", addr, addrType)
	}
	m.mu.Lock()
	if _, exists := m.links[addr]; exists {
		m.mu.Unlock()
		return ConnInfo{}, ErrAlreadyConnected
	}
	if len(m.links) >= MaxConnSlots {
		m.mu.Unlock()
		return ConnInfo{}, ErrNoSlots
	}
	m.mu.Unlock()
	if m.hold != nil {
		m.hold(true)
		defer m.hold(false)
	}
	ctx, cancel := context.WithTimeout(context.Background(), bluezConnectTimeout)
	defer cancel()
	device, err := m.discoverDevice(ctx, addr)
	if err != nil {
		return ConnInfo{}, err
	}
	object := m.bus.Object(bluezBus, device)
	if call := object.CallWithContext(ctx, bluezDevice+".Connect", 0); call.Err != nil && !bluezErrorNamed(call.Err, ".AlreadyConnected", ".InProgress") {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return ConnInfo{}, ErrConnectTimeout
		}
		return ConnInfo{}, fmt.Errorf("ble: BlueZ connect: %w", call.Err)
	}
	for !m.boolProperty(ctx, device, bluezDevice, "Connected") {
		select {
		case <-ctx.Done():
			return ConnInfo{}, ErrConnectTimeout
		case <-time.After(100 * time.Millisecond):
		}
	}
	m.mu.Lock()
	if _, exists := m.links[addr]; exists {
		m.mu.Unlock()
		return ConnInfo{}, ErrAlreadyConnected
	}
	m.links[addr] = &bluezLink{address: addr, device: device}
	m.mu.Unlock()
	return ConnInfo{Addr: addr, MTU: attClientMTU}, nil
}

func (m *BlueZManager) link(addr string) *bluezLink {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.links[strings.ToLower(addr)]
}

func (m *BlueZManager) Disconnect(addr string) error {
	link := m.link(addr)
	if link == nil {
		return ErrNotConnected
	}
	ctx, cancel := context.WithTimeout(context.Background(), bluezCallTimeout)
	defer cancel()
	call := m.bus.Object(bluezBus, link.device).CallWithContext(ctx, bluezDevice+".Disconnect", 0)
	if call.Err != nil && !bluezErrorNamed(call.Err, ".NotConnected") {
		return fmt.Errorf("ble: BlueZ disconnect: %w", call.Err)
	}
	m.removeLink(link.device, bluezDisconnect)
	return nil
}

func (m *BlueZManager) Pair(addr, pin string) (PairInfo, error) {
	if _, err := pairingTK(pin); err != nil {
		return PairInfo{}, err
	}
	link := m.link(addr)
	if link == nil {
		return PairInfo{}, ErrNotConnected
	}
	m.pairMu.Lock()
	defer m.pairMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), smpTimeout)
	defer cancel()
	alreadyPaired := m.boolProperty(ctx, link.device, bluezDevice, "Paired")
	if !alreadyPaired {
		m.mu.Lock()
		m.pair = bluezPairing{device: link.device, pin: pin}
		m.mu.Unlock()
		call := m.bus.Object(bluezBus, link.device).CallWithContext(ctx, bluezDevice+".Pair", 0)
		m.mu.Lock()
		used := m.pair.used
		m.pair = bluezPairing{}
		m.mu.Unlock()
		if call.Err != nil && !bluezErrorNamed(call.Err, ".AlreadyExists") {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return PairInfo{}, errPairTimeout
			}
			return PairInfo{}, fmt.Errorf("ble: BlueZ pairing failed: %w", call.Err)
		}
		if !used {
			_ = m.removeDevice(link.device)
			return PairInfo{}, errors.New("ble: peer did not use authenticated PIN pairing")
		}
	}
	if !m.boolProperty(ctx, link.device, bluezDevice, "Paired") {
		return PairInfo{}, errors.New("ble: BlueZ did not retain the bond")
	}
	trusted := dbus.MakeVariant(true)
	_ = m.bus.Object(bluezBus, link.device).CallWithContext(ctx, bluezProperties+".Set", 0, bluezDevice, "Trusted", trusted).Err
	return PairInfo{Address: link.address, Bonded: true, Encrypted: true, Authenticated: true}, nil
}

func (m *BlueZManager) removeDevice(path dbus.ObjectPath) error {
	ctx, cancel := context.WithTimeout(context.Background(), bluezCallTimeout)
	defer cancel()
	call := m.bus.Object(bluezBus, m.adapter).CallWithContext(ctx, bluezAdapter+".RemoveDevice", 0, path)
	if call.Err != nil && !bluezErrorNamed(call.Err, ".DoesNotExist") {
		return fmt.Errorf("ble: BlueZ forget device: %w", call.Err)
	}
	m.removeLink(path, bluezDisconnect)
	return nil
}

func (m *BlueZManager) ForgetBond(addr string) error {
	addr, ok := normaliseAddr(addr)
	if !ok {
		return fmt.Errorf("ble: bad address %q", addr)
	}
	if link := m.link(addr); link != nil {
		return m.removeDevice(link.device)
	}
	ctx, cancel := context.WithTimeout(context.Background(), bluezCallTimeout)
	defer cancel()
	objects, err := m.managedObjects(ctx)
	if err != nil {
		return err
	}
	if path, exists := m.deviceByAddress(objects, addr); exists {
		return m.removeDevice(path)
	}
	return nil
}

func (m *BlueZManager) waitServices(link *bluezLink) (bluezManagedObjects, error) {
	ctx, cancel := context.WithTimeout(context.Background(), bluezDiscoverTimeout)
	defer cancel()
	for {
		if m.boolProperty(ctx, link.device, bluezDevice, "ServicesResolved") {
			return m.managedObjects(ctx)
		}
		select {
		case <-ctx.Done():
			return nil, ErrATTTimeout
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (m *BlueZManager) Services(addr string) ([]Service, error) {
	link := m.link(addr)
	if link == nil {
		return nil, ErrNotConnected
	}
	objects, err := m.waitServices(link)
	if err != nil {
		return nil, err
	}
	servicePaths := make([]dbus.ObjectPath, 0)
	for path, interfaces := range objects {
		properties, ok := interfaces[bluezService]
		if ok && bluezVariantPath(properties, "Device") == link.device && bluezVariantBool(properties, "Primary") {
			servicePaths = append(servicePaths, path)
		}
	}
	sort.Slice(servicePaths, func(i, j int) bool { return servicePaths[i] < servicePaths[j] })
	handle := uint16(1)
	services := make([]Service, 0, len(servicePaths))
	attributes := map[uint16]bluezAttribute{}
	handleByPath := map[dbus.ObjectPath]uint16{}
	for _, servicePath := range servicePaths {
		serviceProperties := objects[servicePath][bluezService]
		uuid, err := uuidFromString(bluezVariantString(serviceProperties, "UUID"))
		if err != nil {
			return nil, err
		}
		service := Service{Start: handle, UUID: uuid, Characteristics: []Characteristic{}}
		handle++ // synthetic primary-service declaration
		charPaths := make([]dbus.ObjectPath, 0)
		for path, interfaces := range objects {
			properties, ok := interfaces[bluezCharacteristic]
			if ok && bluezVariantPath(properties, "Service") == servicePath {
				charPaths = append(charPaths, path)
			}
		}
		sort.Slice(charPaths, func(i, j int) bool { return charPaths[i] < charPaths[j] })
		for _, charPath := range charPaths {
			properties := objects[charPath][bluezCharacteristic]
			charUUID, err := uuidFromString(bluezVariantString(properties, "UUID"))
			if err != nil {
				return nil, err
			}
			flags := bluezVariantStrings(properties, "Flags")
			props := bluezCharacteristicProperties(flags)
			characteristic := Characteristic{Handle: handle, ValueHandle: handle + 1, UUID: charUUID, Properties: props, Descriptors: []Descriptor{}}
			handle += 2
			attributes[characteristic.ValueHandle] = bluezAttribute{kind: "characteristic", path: charPath, charPath: charPath, indication: props&0x20 != 0 && props&0x10 == 0}
			handleByPath[charPath] = characteristic.ValueHandle
			descPaths := make([]dbus.ObjectPath, 0)
			for path, interfaces := range objects {
				descProperties, ok := interfaces[bluezDescriptor]
				if ok && bluezVariantPath(descProperties, "Characteristic") == charPath {
					descPaths = append(descPaths, path)
				}
			}
			sort.Slice(descPaths, func(i, j int) bool { return descPaths[i] < descPaths[j] })
			hasCCCD := false
			for _, descPath := range descPaths {
				descUUID, err := uuidFromString(bluezVariantString(objects[descPath][bluezDescriptor], "UUID"))
				if err != nil {
					return nil, err
				}
				kind := "descriptor"
				if descUUID.Is16(gattCCCD) {
					kind, hasCCCD = "cccd", true
				}
				characteristic.Descriptors = append(characteristic.Descriptors, Descriptor{Handle: handle, UUID: descUUID})
				attributes[handle] = bluezAttribute{kind: kind, path: descPath, charPath: charPath, indication: props&0x20 != 0 && props&0x10 == 0}
				handle++
			}
			// BlueZ can consume CCCD internally without exposing Descriptor1.
			// Publish a synthetic one so the transport-neutral caller can use the
			// same subscribe-by-handle flow as the direct ATT backend.
			if !hasCCCD && props&(0x10|0x20) != 0 {
				characteristic.Descriptors = append(characteristic.Descriptors, Descriptor{Handle: handle, UUID: UUID16(gattCCCD)})
				attributes[handle] = bluezAttribute{kind: "cccd", charPath: charPath, indication: props&0x20 != 0 && props&0x10 == 0}
				handle++
			}
			service.Characteristics = append(service.Characteristics, characteristic)
		}
		service.End = handle - 1
		services = append(services, service)
	}
	m.mu.Lock()
	if current := m.links[link.address]; current == link {
		link.services, link.attributes, link.handleByPath = services, attributes, handleByPath
	}
	m.mu.Unlock()
	return services, nil
}

func (m *BlueZManager) attribute(addr string, handle uint16) (*bluezLink, bluezAttribute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	link := m.links[strings.ToLower(addr)]
	if link == nil {
		return nil, bluezAttribute{}, ErrNotConnected
	}
	attribute, ok := link.attributes[handle]
	if !ok {
		return nil, bluezAttribute{}, &ATTError{ReqOp: attOpReadReq, Handle: handle, Code: 0x01}
	}
	return link, attribute, nil
}

func (m *BlueZManager) Read(addr string, handle uint16) ([]byte, error) {
	_, attribute, err := m.attribute(addr, handle)
	if err != nil {
		return nil, err
	}
	if attribute.kind == "cccd" {
		notifying := m.boolProperty(context.Background(), attribute.charPath, bluezCharacteristic, "Notifying")
		if notifying {
			if attribute.indication {
				return []byte{0x02, 0x00}, nil
			}
			return []byte{0x01, 0x00}, nil
		}
		return []byte{0x00, 0x00}, nil
	}
	iface := bluezCharacteristic
	if attribute.kind == "descriptor" {
		iface = bluezDescriptor
	}
	ctx, cancel := context.WithTimeout(context.Background(), bluezCallTimeout)
	defer cancel()
	call := m.bus.Object(bluezBus, attribute.path).CallWithContext(ctx, iface+".ReadValue", 0, map[string]dbus.Variant{})
	if call.Err != nil {
		return nil, fmt.Errorf("ble: BlueZ read: %w", call.Err)
	}
	var value []byte
	if err := call.Store(&value); err != nil {
		return nil, fmt.Errorf("ble: decode BlueZ read: %w", err)
	}
	return value, nil
}

func (m *BlueZManager) Write(addr string, handle uint16, value []byte, response bool) error {
	_, attribute, err := m.attribute(addr, handle)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), bluezCallTimeout)
	defer cancel()
	if attribute.kind == "cccd" {
		enable := len(value) >= 2 && (value[0]&0x03) != 0
		method := "StopNotify"
		if enable {
			method = "StartNotify"
		}
		call := m.bus.Object(bluezBus, attribute.charPath).CallWithContext(ctx, bluezCharacteristic+"."+method, 0)
		if call.Err != nil && !(enable && bluezErrorNamed(call.Err, ".InProgress")) {
			return fmt.Errorf("ble: BlueZ %s: %w", method, call.Err)
		}
		return nil
	}
	iface := bluezCharacteristic
	if attribute.kind == "descriptor" {
		iface = bluezDescriptor
	}
	writeType := "command"
	if response {
		writeType = "request"
	}
	options := map[string]dbus.Variant{"type": dbus.MakeVariant(writeType)}
	call := m.bus.Object(bluezBus, attribute.path).CallWithContext(ctx, iface+".WriteValue", 0, append([]byte(nil), value...), options)
	if call.Err != nil {
		return fmt.Errorf("ble: BlueZ write: %w", call.Err)
	}
	return nil
}

func (m *BlueZManager) Connected() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.links))
	for address := range m.links {
		out = append(out, address)
	}
	sort.Strings(out)
	return out
}

func (m *BlueZManager) Slots() (free, limit int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return MaxConnSlots - len(m.links), MaxConnSlots
}

func (m *BlueZManager) removeLink(path dbus.ObjectPath, reason byte) {
	m.mu.Lock()
	var address string
	for candidate, link := range m.links {
		if link.device == path {
			address = candidate
			delete(m.links, candidate)
			break
		}
	}
	callback := m.onDisconnect
	m.mu.Unlock()
	if address != "" && callback != nil {
		callback(address, reason)
	}
}

func (m *BlueZManager) signalLoop(signals <-chan *dbus.Signal) {
	for signal := range signals {
		if signal == nil || signal.Name != bluezProperties+".PropertiesChanged" || len(signal.Body) < 2 {
			continue
		}
		iface, _ := signal.Body[0].(string)
		changed, _ := signal.Body[1].(map[string]dbus.Variant)
		switch iface {
		case bluezDevice:
			if value, ok := changed["Connected"]; ok {
				connected, _ := value.Value().(bool)
				if !connected {
					m.removeLink(signal.Path, bluezDisconnect)
				}
			}
		case bluezCharacteristic:
			valueVariant, ok := changed["Value"]
			if !ok {
				continue
			}
			value, ok := valueVariant.Value().([]byte)
			if !ok {
				continue
			}
			m.mu.Lock()
			var address string
			var handle uint16
			var indication bool
			for candidate, link := range m.links {
				if found, exists := link.handleByPath[signal.Path]; exists {
					address, handle = candidate, found
					indication = link.attributes[found].indication
					break
				}
			}
			callback := m.onNotify
			m.mu.Unlock()
			if address != "" && callback != nil {
				callback(address, handle, append([]byte(nil), value...), indication)
			}
		}
	}
}

func (m *BlueZManager) agentPIN(device dbus.ObjectPath) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pair.device != device || m.pair.pin == "" {
		return "", false
	}
	m.pair.used = true
	return m.pair.pin, true
}

func (m *BlueZManager) agentAuthorized(device dbus.ObjectPath) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pair.device == device && m.pair.pin != "" && m.pair.used
}

func bluezRejected(message string) *dbus.Error {
	return dbus.NewError("org.bluez.Error.Rejected", []any{message})
}

func (a *bluezPairingAgent) Release() *dbus.Error { return nil }

func (a *bluezPairingAgent) RequestPinCode(device dbus.ObjectPath) (string, *dbus.Error) {
	if pin, ok := a.manager.agentPIN(device); ok {
		return pin, nil
	}
	return "", bluezRejected("No Tater PIN pairing is active for this device")
}

func (a *bluezPairingAgent) RequestPasskey(device dbus.ObjectPath) (uint32, *dbus.Error) {
	pin, ok := a.manager.agentPIN(device)
	if !ok {
		return 0, bluezRejected("No Tater PIN pairing is active for this device")
	}
	value, err := strconv.ParseUint(pin, 10, 32)
	if err != nil {
		return 0, bluezRejected("Invalid Tater PIN")
	}
	return uint32(value), nil
}

func (a *bluezPairingAgent) DisplayPinCode(dbus.ObjectPath, string) *dbus.Error {
	return bluezRejected("Tater requires PIN entry, not display-only pairing")
}

func (a *bluezPairingAgent) DisplayPasskey(dbus.ObjectPath, uint32, uint16) *dbus.Error {
	return bluezRejected("Tater requires PIN entry, not display-only pairing")
}

func (a *bluezPairingAgent) RequestConfirmation(device dbus.ObjectPath, passkey uint32) *dbus.Error {
	pin, ok := a.manager.agentPIN(device)
	if !ok {
		return bluezRejected("No Tater PIN pairing is active for this device")
	}
	want, err := strconv.ParseUint(pin, 10, 32)
	if err != nil || uint32(want) != passkey {
		return bluezRejected("Meshtastic passkey did not match")
	}
	return nil
}

func (a *bluezPairingAgent) RequestAuthorization(device dbus.ObjectPath) *dbus.Error {
	if !a.manager.agentAuthorized(device) {
		return bluezRejected("The device has not completed authenticated PIN entry")
	}
	return nil
}

func (a *bluezPairingAgent) AuthorizeService(device dbus.ObjectPath, _ string) *dbus.Error {
	if a.manager.linkAddressForPath(device) == "" {
		return bluezRejected("Device is not connected through Tater")
	}
	return nil
}

func (a *bluezPairingAgent) Cancel() *dbus.Error { return nil }

func (m *BlueZManager) linkAddressForPath(path dbus.ObjectPath) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for address, link := range m.links {
		if link.device == path {
			return address
		}
	}
	return ""
}
