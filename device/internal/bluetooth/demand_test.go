package bluetooth

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type demandRadio struct {
	mu          sync.Mutex
	power, scan bool
	transitions []bool
	onDisable   func()
}

func (r *demandRadio) SetEnabled(on bool) {
	r.mu.Lock()
	r.power = on
	r.transitions = append(r.transitions, on)
	r.mu.Unlock()
	if !on && r.onDisable != nil {
		r.onDisable()
	}
}
func (r *demandRadio) SetScanning(on bool) { r.mu.Lock(); r.scan = on; r.mu.Unlock() }

type demandBackend struct {
	mu                         sync.Mutex
	links                      []string
	powered                    bool
	powerCalls                 int
	powerStarted, powerRelease chan struct{}
	disconnectError            bool
}

func (m *demandBackend) SetPowered(v bool) error {
	if v && m.powerRelease != nil {
		close(m.powerStarted)
		<-m.powerRelease
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.powered = v
	m.powerCalls++
	return nil
}
func (m *demandBackend) SetCallbacks(func(string, uint16, []byte, bool), func(string, byte)) {}
func (m *demandBackend) Connected() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.links...)
}
func (m *demandBackend) Slots() (int, int) { return 2 - len(m.Connected()), 2 }
func (m *demandBackend) Connect(a string, _ int) (ConnInfo, error) {
	m.mu.Lock()
	m.links = append(m.links, a)
	m.mu.Unlock()
	return ConnInfo{}, nil
}
func (m *demandBackend) Disconnect(a string) error {
	if m.disconnectError {
		return ErrDisconnected
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, v := range m.links {
		if v == a {
			m.links = append(m.links[:i], m.links[i+1:]...)
			break
		}
	}
	return nil
}
func (m *demandBackend) Pair(string, string) (PairInfo, error)    { return PairInfo{}, nil }
func (m *demandBackend) ForgetBond(string) error                  { return nil }
func (m *demandBackend) Services(string) ([]Service, error)       { return nil, nil }
func (m *demandBackend) Read(string, uint16) ([]byte, error)      { return nil, nil }
func (m *demandBackend) Write(string, uint16, []byte, bool) error { return nil }
func fixtureDemand(t *testing.T) (*DemandManager, *demandBackend, *demandRadio) {
	t.Helper()
	m, r := &demandBackend{}, &demandRadio{}
	d := NewDemandManager(m, r)
	t.Cleanup(d.Close)
	return d, m, r
}
func TestDemandInitiallyOffAndSlotsNeverWakeRadio(t *testing.T) {
	d, m, r := fixtureDemand(t)
	d.Slots()
	d.poll()
	m.mu.Lock()
	defer m.mu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if m.powered || r.power || r.scan {
		t.Fatal("unused Bluetooth must stay off")
	}
}
func TestDemandScanExpiryDoesNotDropActiveLink(t *testing.T) {
	d, m, r := fixtureDemand(t)
	if _, err := d.Connect("aa:bb:cc:dd:ee:ff", 1); err != nil {
		t.Fatal(err)
	}
	if err := d.Discover(5); err != nil {
		t.Fatal(err)
	}
	d.StopDiscovery()
	d.mu.Lock()
	d.idleSince = time.Now().Add(-4 * time.Second)
	d.mu.Unlock()
	d.poll()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.scan || !r.power || len(m.Connected()) != 1 {
		t.Fatal("stopping discovery must preserve the active connection")
	}
}
func TestDemandUnusedRadioPowersOffAfterLease(t *testing.T) {
	d, _, r := fixtureDemand(t)
	if err := d.Discover(5); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	d.until = time.Now().Add(-time.Second)
	d.mu.Unlock()
	d.poll()
	d.mu.Lock()
	d.idleSince = time.Now().Add(-4 * time.Second)
	d.mu.Unlock()
	d.poll()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.scan || r.power {
		t.Fatal("expired discovery left the radio running")
	}
}
func TestDemandVoicePausesDiscoveryAndInvalidDurationsDoNotWake(t *testing.T) {
	d, _, r := fixtureDemand(t)
	for _, seconds := range []int{0, 4, 61} {
		if d.Discover(seconds) == nil {
			t.Fatal("invalid discovery duration accepted")
		}
	}
	if err := d.Discover(5); err != nil {
		t.Fatal(err)
	}
	d.SetBusy(true)
	r.mu.Lock()
	scanning := r.scan
	r.mu.Unlock()
	if scanning {
		t.Fatal("discovery continued during voice")
	}
	d.SetBusy(false)
	r.mu.Lock()
	scanning = r.scan
	r.mu.Unlock()
	if !scanning {
		t.Fatal("unexpired discovery did not resume")
	}
}

func TestDemandDisconnectDuringPowerUpCannotRestartDiscovery(t *testing.T) {
	m := &demandBackend{powerStarted: make(chan struct{}), powerRelease: make(chan struct{})}
	r := &demandRadio{}
	d := NewDemandManager(m, r)
	t.Cleanup(d.Close)
	result := make(chan error, 1)
	go func() { result <- d.Discover(15) }()
	<-m.powerStarted
	reset := make(chan struct{})
	go func() { d.Reset(); close(reset) }()
	deadline := time.Now().Add(time.Second)
	for d.epoch.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(m.powerRelease)
	if err := <-result; !errors.Is(err, ErrDisconnected) {
		t.Fatalf("discovery survived reset: %v", err)
	}
	<-reset
	_, scanning := d.RadioStatus()
	if scanning {
		t.Fatal("controller disconnect restarted discovery")
	}
}

func TestDemandCloseAllowsSynchronousControllerCallbacks(t *testing.T) {
	d, m, r := fixtureDemand(t)
	if _, err := d.Connect("aa:bb:cc:dd:ee:ff", 0); err != nil {
		t.Fatal(err)
	}
	m.disconnectError = true
	r.onDisable = func() { d.RadioStatus() }
	done := make(chan struct{})
	go func() { d.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("controller shutdown deadlocked its status callback")
	}
}
