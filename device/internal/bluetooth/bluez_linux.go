//go:build linux && !android

package bluetooth

// Tater Linux targets expose the controller as hci0, which bluetoothd owns.
// Active GATT must therefore use BlueZ's D-Bus API;
// issuing LE Create Connection or ATT packets on the raw monitor socket would
// race bluetoothd's connection and security state.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
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
	bluezConnectTries    = 3
	bluezConnectRetry    = 500 * time.Millisecond
)

type bluezManagedObjects map[dbus.ObjectPath]map[string]map[string]dbus.Variant

type bluezAttribute struct {
	kind       string
	path       dbus.ObjectPath
	charPath   dbus.ObjectPath
	indication bool
}

type bluezLink struct {
	address       string
	device        dbus.ObjectPath
	services      []Service
	attributes    map[uint16]bluezAttribute
	handleByPath  map[dbus.ObjectPath]uint16
	notifications map[uint16]*bluezNotification
}

type bluezNotification struct {
	viaSignal bool
	stop      func(unsubscribe bool)
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
	if err := bus.AddMatchSignal(
		dbus.WithMatchSender(bluezBus),
		dbus.WithMatchInterface(bluezObjectManager),
		dbus.WithMatchMember("InterfacesRemoved"),
	); err != nil {
		return fail(fmt.Errorf("ble: subscribe to BlueZ object removal: %w", err))
	}
	if err := bus.AddMatchSignal(
		dbus.WithMatchSender("org.freedesktop.DBus"),
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, bluezBus),
	); err != nil {
		return fail(fmt.Errorf("ble: subscribe to BlueZ restarts: %w", err))
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

func bluezVariantUint16(properties map[string]dbus.Variant, name string) uint16 {
	if value, ok := properties[name]; ok {
		if number, ok := value.Value().(uint16); ok {
			return number
		}
	}
	return 0
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
	name := ""
	if errors.As(err, &dbusErr) {
		name = dbusErr.Name
	} else {
		var dbusErrPointer *dbus.Error
		if errors.As(err, &dbusErrPointer) && dbusErrPointer != nil {
			name = dbusErrPointer.Name
		}
	}
	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func bluezPathHandle(path dbus.ObjectPath) (uint16, error) {
	name := string(path)
	if slash := strings.LastIndexByte(name, '/'); slash >= 0 {
		name = name[slash+1:]
	}
	for _, prefix := range []string{"service", "char", "desc"} {
		if strings.HasPrefix(name, prefix) {
			hex := strings.TrimPrefix(name, prefix)
			if len(hex) != 4 {
				break
			}
			value, err := strconv.ParseUint(hex, 16, 16)
			if err == nil && value != 0 {
				return uint16(value), nil
			}
			break
		}
	}
	return 0, fmt.Errorf("ble: BlueZ object %q has no ATT handle", path)
}

func bluezConnectRetryable(err error) bool {
	return bluezErrorNamed(err, ".Failed")
}

func bluezDiscoveryFilterUnsupported(err error) bool {
	return bluezErrorNamed(err, ".NotSupported")
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
		// BlueZ gates SetDiscoveryFilter on kernel management protocol 1.8.
		// Older Radar/Biscuit kernels reject the method itself, regardless of
		// which filter keys are present, but unfiltered discovery still works.
		if !bluezDiscoveryFilterUnsupported(call.Err) {
			return "", fmt.Errorf("ble: set BlueZ discovery filter: %w", call.Err)
		}
		log.Printf("ble: BlueZ discovery filters are unsupported; using unfiltered discovery")
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

func (m *BlueZManager) connectDevice(ctx context.Context, addr string) (dbus.ObjectPath, error) {
	var lastErr error
	for attempt := 1; attempt <= bluezConnectTries; attempt++ {
		if m.hold != nil {
			m.hold(true)
		}
		device, err := m.discoverDevice(ctx, addr)
		if err == nil {
			call := m.bus.Object(bluezBus, device).CallWithContext(ctx, bluezDevice+".Connect", 0)
			err = call.Err
			if bluezErrorNamed(err, ".AlreadyConnected", ".InProgress") {
				err = nil
			}
		}
		if m.hold != nil {
			m.hold(false)
		}
		if err == nil {
			return device, nil
		}
		lastErr = err
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", ErrConnectTimeout
		}
		if attempt == bluezConnectTries || !bluezConnectRetryable(err) {
			break
		}
		log.Printf("ble: BlueZ connect attempt %d/%d failed transiently: %v", attempt, bluezConnectTries, err)
		timer := time.NewTimer(bluezConnectRetry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ErrConnectTimeout
		case <-timer.C:
		}
	}
	return "", fmt.Errorf("ble: BlueZ connect: %w", lastErr)
}

func (m *BlueZManager) disconnectDevice(path dbus.ObjectPath) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = m.bus.Object(bluezBus, path).CallWithContext(ctx, bluezDevice+".Disconnect", 0).Err
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
	ctx, cancel := context.WithTimeout(context.Background(), bluezConnectTimeout)
	defer cancel()
	device, err := m.connectDevice(ctx, addr)
	if err != nil {
		return ConnInfo{}, err
	}
	for !m.boolProperty(ctx, device, bluezDevice, "Connected") {
		select {
		case <-ctx.Done():
			return ConnInfo{}, ErrConnectTimeout
		case <-time.After(100 * time.Millisecond):
		}
	}
	link := &bluezLink{address: addr, device: device, notifications: map[uint16]*bluezNotification{}}
	objects, err := m.waitServicesContext(ctx, link)
	if err != nil {
		m.disconnectDevice(device)
		return ConnInfo{}, err
	}
	link.services, link.attributes, link.handleByPath, err = bluezBuildServices(objects, device)
	if err != nil {
		m.disconnectDevice(device)
		return ConnInfo{}, err
	}
	mtu := m.connectionMTU(ctx, objects, link)
	if !m.boolProperty(ctx, device, bluezDevice, "Connected") {
		return ConnInfo{}, ErrNotConnected
	}
	m.mu.Lock()
	if _, exists := m.links[addr]; exists {
		m.mu.Unlock()
		m.disconnectDevice(device)
		return ConnInfo{}, ErrAlreadyConnected
	}
	m.links[addr] = link
	m.mu.Unlock()
	return ConnInfo{Addr: addr, MTU: mtu}, nil
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

func (m *BlueZManager) waitServicesContext(ctx context.Context, link *bluezLink) (bluezManagedObjects, error) {
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

func (m *BlueZManager) waitServices(link *bluezLink) (bluezManagedObjects, error) {
	ctx, cancel := context.WithTimeout(context.Background(), bluezDiscoverTimeout)
	defer cancel()
	return m.waitServicesContext(ctx, link)
}

func bluezBuildServices(objects bluezManagedObjects, device dbus.ObjectPath) ([]Service, map[uint16]bluezAttribute, map[dbus.ObjectPath]uint16, error) {
	serviceProperties := map[dbus.ObjectPath]map[string]dbus.Variant{}
	for path, interfaces := range objects {
		properties, ok := interfaces[bluezService]
		if ok && bluezVariantPath(properties, "Device") == device {
			serviceProperties[path] = properties
		}
	}
	characteristicProperties := map[dbus.ObjectPath]map[string]dbus.Variant{}
	for path, interfaces := range objects {
		properties, ok := interfaces[bluezCharacteristic]
		if !ok {
			continue
		}
		if _, belongs := serviceProperties[bluezVariantPath(properties, "Service")]; belongs {
			characteristicProperties[path] = properties
		}
	}
	descriptorProperties := map[dbus.ObjectPath]map[string]dbus.Variant{}
	for path, interfaces := range objects {
		properties, ok := interfaces[bluezDescriptor]
		if !ok {
			continue
		}
		if _, belongs := characteristicProperties[bluezVariantPath(properties, "Characteristic")]; belongs {
			descriptorProperties[path] = properties
		}
	}

	occupied := map[uint16]bool{}
	allServiceHandles := make([]uint16, 0, len(serviceProperties))
	for path := range serviceProperties {
		handle, err := bluezPathHandle(path)
		if err != nil {
			return nil, nil, nil, err
		}
		occupied[handle] = true
		allServiceHandles = append(allServiceHandles, handle)
	}
	for path := range characteristicProperties {
		handle, err := bluezPathHandle(path)
		if err != nil || handle == 0xffff {
			if err == nil {
				err = fmt.Errorf("ble: BlueZ characteristic %q has invalid handle", path)
			}
			return nil, nil, nil, err
		}
		occupied[handle], occupied[handle+1] = true, true
	}
	for path := range descriptorProperties {
		handle, err := bluezPathHandle(path)
		if err != nil {
			return nil, nil, nil, err
		}
		occupied[handle] = true
	}
	sort.Slice(allServiceHandles, func(i, j int) bool { return allServiceHandles[i] < allServiceHandles[j] })

	servicePaths := make([]dbus.ObjectPath, 0, len(serviceProperties))
	for path, properties := range serviceProperties {
		if bluezVariantBool(properties, "Primary") {
			servicePaths = append(servicePaths, path)
		}
	}
	sort.Slice(servicePaths, func(i, j int) bool {
		left, _ := bluezPathHandle(servicePaths[i])
		right, _ := bluezPathHandle(servicePaths[j])
		return left < right
	})

	services := make([]Service, 0, len(servicePaths))
	attributes := map[uint16]bluezAttribute{}
	handleByPath := map[dbus.ObjectPath]uint16{}
	for _, servicePath := range servicePaths {
		serviceHandle, _ := bluezPathHandle(servicePath)
		serviceLimit := uint16(0xffff)
		serviceEnd := serviceHandle
		for _, next := range allServiceHandles {
			if next > serviceHandle {
				serviceLimit, serviceEnd = next-1, next-1
				break
			}
		}
		uuid, err := uuidFromString(bluezVariantString(serviceProperties[servicePath], "UUID"))
		if err != nil {
			return nil, nil, nil, err
		}
		service := Service{Start: serviceHandle, End: serviceEnd, UUID: uuid, Characteristics: []Characteristic{}}
		charPaths := make([]dbus.ObjectPath, 0)
		for path, properties := range characteristicProperties {
			if bluezVariantPath(properties, "Service") == servicePath {
				charPaths = append(charPaths, path)
			}
		}
		sort.Slice(charPaths, func(i, j int) bool {
			left, _ := bluezPathHandle(charPaths[i])
			right, _ := bluezPathHandle(charPaths[j])
			return left < right
		})
		for charIndex, charPath := range charPaths {
			properties := characteristicProperties[charPath]
			charUUID, err := uuidFromString(bluezVariantString(properties, "UUID"))
			if err != nil {
				return nil, nil, nil, err
			}
			charHandle, _ := bluezPathHandle(charPath)
			valueHandle := charHandle + 1
			flags := bluezVariantStrings(properties, "Flags")
			props := bluezCharacteristicProperties(flags)
			characteristic := Characteristic{Handle: charHandle, ValueHandle: valueHandle, UUID: charUUID, Properties: props, Descriptors: []Descriptor{}}
			if _, duplicate := attributes[valueHandle]; duplicate {
				return nil, nil, nil, fmt.Errorf("ble: duplicate BlueZ ATT handle 0x%04x", valueHandle)
			}
			attributes[valueHandle] = bluezAttribute{kind: "characteristic", path: charPath, charPath: charPath, indication: props&0x20 != 0 && props&0x10 == 0}
			handleByPath[charPath] = valueHandle
			descPaths := make([]dbus.ObjectPath, 0)
			for path, properties := range descriptorProperties {
				if bluezVariantPath(properties, "Characteristic") == charPath {
					descPaths = append(descPaths, path)
				}
			}
			sort.Slice(descPaths, func(i, j int) bool {
				left, _ := bluezPathHandle(descPaths[i])
				right, _ := bluezPathHandle(descPaths[j])
				return left < right
			})
			hasCCCD := false
			for _, descPath := range descPaths {
				descUUID, err := uuidFromString(bluezVariantString(descriptorProperties[descPath], "UUID"))
				if err != nil {
					return nil, nil, nil, err
				}
				descHandle, _ := bluezPathHandle(descPath)
				kind := "descriptor"
				if descUUID.Is16(gattCCCD) {
					kind, hasCCCD = "cccd", true
				}
				if _, duplicate := attributes[descHandle]; duplicate {
					return nil, nil, nil, fmt.Errorf("ble: duplicate BlueZ ATT handle 0x%04x", descHandle)
				}
				characteristic.Descriptors = append(characteristic.Descriptors, Descriptor{Handle: descHandle, UUID: descUUID})
				attributes[descHandle] = bluezAttribute{kind: kind, path: descPath, charPath: charPath, indication: props&0x20 != 0 && props&0x10 == 0}
				if descHandle > service.End {
					service.End = descHandle
				}
			}
			// BlueZ can consume CCCD internally without exposing Descriptor1.
			// Infer its real handle from the gap between BlueZ ATT object paths.
			if !hasCCCD && props&(0x10|0x20) != 0 {
				candidate := valueHandle + 1
				limit := serviceLimit
				if charIndex+1 < len(charPaths) {
					next, _ := bluezPathHandle(charPaths[charIndex+1])
					limit = next - 1
				}
				for candidate != 0 && candidate <= limit && occupied[candidate] {
					if candidate == limit {
						candidate = 0
						break
					}
					candidate++
				}
				if candidate == 0 || candidate > limit {
					return nil, nil, nil, fmt.Errorf("ble: no ATT handle available for BlueZ CCCD after 0x%04x", valueHandle)
				}
				occupied[candidate] = true
				characteristic.Descriptors = append(characteristic.Descriptors, Descriptor{Handle: candidate, UUID: UUID16(gattCCCD)})
				attributes[candidate] = bluezAttribute{kind: "cccd", charPath: charPath, indication: props&0x20 != 0 && props&0x10 == 0}
				if candidate > service.End {
					service.End = candidate
				}
			}
			if valueHandle > service.End {
				service.End = valueHandle
			}
			service.Characteristics = append(service.Characteristics, characteristic)
		}
		services = append(services, service)
	}
	return services, attributes, handleByPath, nil
}

func (m *BlueZManager) connectionMTU(ctx context.Context, objects bluezManagedObjects, link *bluezLink) int {
	var first dbus.ObjectPath
	for path := range link.handleByPath {
		if first == "" {
			first = path
		}
		if mtu := bluezVariantUint16(objects[path][bluezCharacteristic], "MTU"); mtu >= attDefaultMTU && mtu <= 517 {
			return int(mtu)
		}
	}
	if first != "" {
		if value, err := m.property(ctx, first, bluezCharacteristic, "MTU"); err == nil {
			if mtu, ok := value.Value().(uint16); ok && mtu >= attDefaultMTU && mtu <= 517 {
				return int(mtu)
			}
		}
	}
	return attDefaultMTU
}

func (m *BlueZManager) Services(addr string) ([]Service, error) {
	link := m.link(addr)
	if link == nil {
		return nil, ErrNotConnected
	}
	m.mu.Lock()
	if link.services != nil {
		services := append([]Service(nil), link.services...)
		m.mu.Unlock()
		return services, nil
	}
	m.mu.Unlock()
	objects, err := m.waitServices(link)
	if err != nil {
		return nil, err
	}
	services, attributes, handleByPath, err := bluezBuildServices(objects, link.device)
	if err != nil {
		return nil, err
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

func (m *BlueZManager) installNotification(link *bluezLink, handle uint16, notification *bluezNotification) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.links[link.address] != link {
		return false
	}
	if link.notifications == nil {
		link.notifications = map[uint16]*bluezNotification{}
	}
	if link.notifications[handle] != nil {
		return false
	}
	link.notifications[handle] = notification
	return true
}

func (m *BlueZManager) notificationEnabled(link *bluezLink, handle uint16) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.links[link.address] == link && link.notifications[handle] != nil
}

func (m *BlueZManager) dropNotification(link *bluezLink, handle uint16, notification *bluezNotification, unsubscribe bool) {
	m.mu.Lock()
	if m.links[link.address] == link && link.notifications[handle] == notification {
		delete(link.notifications, handle)
	}
	m.mu.Unlock()
	if notification != nil && notification.stop != nil {
		notification.stop(unsubscribe)
	}
}

func (m *BlueZManager) relayAcquiredNotifications(link *bluezLink, handle uint16, indication bool, notification *bluezNotification, file *os.File, mtu uint16) {
	bufferSize := int(mtu)
	if bufferSize < attDefaultMTU || bufferSize > 517 {
		bufferSize = 512
	}
	buffer := make([]byte, bufferSize)
	for {
		n, err := file.Read(buffer)
		if n > 0 {
			m.mu.Lock()
			active := m.links[link.address] == link && link.notifications[handle] == notification
			callback := m.onNotify
			m.mu.Unlock()
			if active && callback != nil {
				callback(link.address, handle, append([]byte(nil), buffer[:n]...), indication)
			}
		}
		if err != nil {
			m.dropNotification(link, handle, notification, false)
			return
		}
	}
}

func (m *BlueZManager) subscribe(link *bluezLink, handle uint16, attribute bluezAttribute) error {
	if m.notificationEnabled(link, handle) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), bluezCallTimeout)
	defer cancel()
	object := m.bus.Object(bluezBus, attribute.charPath)
	call := object.CallWithContext(ctx, bluezCharacteristic+".AcquireNotify", 0, map[string]dbus.Variant{})
	if call.Err == nil {
		var fd dbus.UnixFD
		var mtu uint16
		if err := call.Store(&fd, &mtu); err != nil {
			return fmt.Errorf("ble: decode BlueZ notification socket: %w", err)
		}
		rawFD := int(fd)
		if err := unix.SetNonblock(rawFD, true); err != nil {
			_ = unix.Close(rawFD)
			return fmt.Errorf("ble: configure BlueZ notification socket: %w", err)
		}
		file := os.NewFile(uintptr(rawFD), "bluez-notify")
		if file == nil {
			_ = unix.Close(rawFD)
			return errors.New("ble: open BlueZ notification socket")
		}
		notification := &bluezNotification{stop: func(bool) { _ = file.Close() }}
		if !m.installNotification(link, handle, notification) {
			_ = file.Close()
			if m.notificationEnabled(link, handle) {
				return nil
			}
			return ErrNotConnected
		}
		go m.relayAcquiredNotifications(link, handle, attribute.indication, notification, file, mtu)
		return nil
	}

	// AcquireNotify is optional. StartNotify remains the compatible fallback
	// and is tracked so ordinary ReadValue property updates are not mistaken
	// for unsolicited notifications when no subscription exists.
	notification := &bluezNotification{viaSignal: true}
	notification.stop = func(unsubscribe bool) {
		if !unsubscribe {
			return
		}
		stopCtx, stopCancel := context.WithTimeout(context.Background(), bluezCallTimeout)
		defer stopCancel()
		_ = object.CallWithContext(stopCtx, bluezCharacteristic+".StopNotify", 0).Err
	}
	if !m.installNotification(link, handle, notification) {
		if m.notificationEnabled(link, handle) {
			return nil
		}
		return ErrNotConnected
	}
	start := object.CallWithContext(ctx, bluezCharacteristic+".StartNotify", 0)
	if start.Err != nil && !bluezErrorNamed(start.Err, ".InProgress") {
		m.dropNotification(link, handle, notification, false)
		return fmt.Errorf("ble: BlueZ StartNotify: %w", start.Err)
	}
	return nil
}

func (m *BlueZManager) unsubscribe(link *bluezLink, handle uint16) {
	m.mu.Lock()
	var notification *bluezNotification
	if m.links[link.address] == link {
		notification = link.notifications[handle]
		delete(link.notifications, handle)
	}
	m.mu.Unlock()
	if notification != nil && notification.stop != nil {
		notification.stop(true)
	}
}

func (m *BlueZManager) Read(addr string, handle uint16) ([]byte, error) {
	link, attribute, err := m.attribute(addr, handle)
	if err != nil {
		return nil, err
	}
	if attribute.kind == "cccd" {
		if m.notificationEnabled(link, handle) {
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
	link, attribute, err := m.attribute(addr, handle)
	if err != nil {
		return err
	}
	if attribute.kind == "cccd" {
		enable := len(value) >= 2 && (value[0]&0x03) != 0
		if enable {
			return m.subscribe(link, handle, attribute)
		}
		m.unsubscribe(link, handle)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), bluezCallTimeout)
	defer cancel()
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
	m.removeLinks(func(link *bluezLink) bool { return link.device == path }, reason)
}

func (m *BlueZManager) removeLinks(matches func(*bluezLink) bool, reason byte) {
	type removedLink struct {
		address       string
		notifications []*bluezNotification
	}
	m.mu.Lock()
	removed := make([]removedLink, 0)
	for candidate, link := range m.links {
		if !matches(link) {
			continue
		}
		entry := removedLink{address: candidate}
		for _, notification := range link.notifications {
			entry.notifications = append(entry.notifications, notification)
		}
		removed = append(removed, entry)
		delete(m.links, candidate)
	}
	callback := m.onDisconnect
	m.mu.Unlock()
	for _, link := range removed {
		for _, notification := range link.notifications {
			if notification != nil && notification.stop != nil {
				notification.stop(false)
			}
		}
		if callback != nil {
			callback(link.address, reason)
		}
	}
}

func (m *BlueZManager) handleSignal(signal *dbus.Signal) {
	if signal == nil {
		return
	}
	switch signal.Name {
	case bluezProperties + ".PropertiesChanged":
		if len(signal.Body) < 2 {
			return
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
				return
			}
			value, ok := valueVariant.Value().([]byte)
			if !ok {
				return
			}
			m.mu.Lock()
			var address string
			var handle uint16
			var indication bool
			for candidate, link := range m.links {
				if found, exists := link.handleByPath[signal.Path]; exists {
					notification := link.notifications[found]
					if notification != nil && notification.viaSignal {
						address, handle = candidate, found
						indication = link.attributes[found].indication
					}
					break
				}
			}
			callback := m.onNotify
			m.mu.Unlock()
			if address != "" && callback != nil {
				callback(address, handle, append([]byte(nil), value...), indication)
			}
		}
	case bluezObjectManager + ".InterfacesRemoved":
		if len(signal.Body) < 2 {
			return
		}
		path, _ := signal.Body[0].(dbus.ObjectPath)
		interfaces, _ := signal.Body[1].([]string)
		adapterRemoved, deviceRemoved := false, false
		for _, iface := range interfaces {
			adapterRemoved = adapterRemoved || iface == bluezAdapter
			deviceRemoved = deviceRemoved || iface == bluezDevice
		}
		if adapterRemoved && path == m.adapter {
			m.removeLinks(func(*bluezLink) bool { return true }, bluezDisconnect)
		} else if deviceRemoved {
			m.removeLink(path, bluezDisconnect)
		}
	case "org.freedesktop.DBus.NameOwnerChanged":
		if len(signal.Body) < 3 {
			return
		}
		name, _ := signal.Body[0].(string)
		oldOwner, _ := signal.Body[1].(string)
		if name == bluezBus && oldOwner != "" {
			m.removeLinks(func(*bluezLink) bool { return true }, bluezDisconnect)
		}
	}
}

func (m *BlueZManager) signalLoop(signals <-chan *dbus.Signal) {
	for signal := range signals {
		m.handleSignal(signal)
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
