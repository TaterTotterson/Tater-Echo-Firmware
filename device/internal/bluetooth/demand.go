package bluetooth

// DemandManager separates controller lifetime, discovery, and active links.
// A remembered bond alone never keeps the radio or a scan running.
import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type DemandScanner interface {
	SetEnabled(bool)
	SetScanning(bool)
}

type DemandManager struct {
	base       GATTManager
	scanner    DemandScanner
	mu         sync.Mutex
	operations int
	until      time.Time
	idleSince  time.Time
	powered    atomic.Bool
	busy       atomic.Bool
	closed     atomic.Bool
	epoch      atomic.Uint64
	stop       chan struct{}
	closeOnce  sync.Once
}

func NewDemandManager(base GATTManager, scanner DemandScanner) *DemandManager {
	d := &DemandManager{base: base, scanner: scanner, stop: make(chan struct{})}
	scanner.SetScanning(false)
	// The runtime owns this adapter. Saved BlueZ bonds remain on disk.
	if p, ok := base.(interface{ SetPowered(bool) error }); ok {
		_ = p.SetPowered(false)
	}
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				d.poll()
			case <-d.stop:
				return
			}
		}
	}()
	return d
}
func (d *DemandManager) SetBusy(busy bool) { d.busy.Store(busy); d.poll() }
func (d *DemandManager) power(on bool) error {
	if on == d.powered.Load() {
		return nil
	}
	if p, ok := d.base.(interface{ SetPowered(bool) error }); ok {
		if err := p.SetPowered(on); err != nil {
			return err
		}
	}
	d.scanner.SetEnabled(on)
	d.powered.Store(on)
	return nil
}
func (d *DemandManager) begin() error {
	d.mu.Lock()
	if d.closed.Load() {
		d.mu.Unlock()
		return fmt.Errorf("Bluetooth proxy is closed")
	}
	d.operations++
	d.idleSince = time.Time{}
	err := d.power(true)
	d.mu.Unlock()
	if err != nil {
		d.end()
		return err
	}
	// Raw HCI attaches asynchronously. BlueZ has its own synchronous power-on.
	if _, bluez := d.base.(interface{ SetPowered(bool) error }); !bluez {
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if ready, ok := d.base.(interface{ Ready() bool }); ok && ready.Ready() {
				return nil
			}
			time.Sleep(20 * time.Millisecond)
		}
		d.end()
		return fmt.Errorf("Bluetooth controller did not become ready")
	}
	return nil
}
func (d *DemandManager) end() {
	d.mu.Lock()
	d.operations--
	d.mu.Unlock()
	d.poll()
}
func (d *DemandManager) poll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	discovering := time.Now().Before(d.until)
	d.scanner.SetScanning(discovering && !d.busy.Load())
	if !d.powered.Load() || discovering || d.operations > 0 || len(d.base.Connected()) > 0 {
		d.idleSince = time.Time{}
		return
	}
	if d.idleSince.IsZero() {
		d.idleSince = time.Now()
		return
	}
	if time.Since(d.idleSince) >= 3*time.Second {
		_ = d.power(false)
	}
}
func (d *DemandManager) Discover(seconds int) error {
	if seconds < 5 || seconds > 60 {
		return fmt.Errorf("discovery must last 5–60 seconds")
	}
	epoch := d.epoch.Load()
	if err := d.begin(); err != nil {
		return err
	}
	defer d.end()
	d.mu.Lock()
	if d.epoch.Load() != epoch || d.closed.Load() {
		d.mu.Unlock()
		return ErrDisconnected
	}
	d.until = time.Now().Add(time.Duration(seconds) * time.Second)
	d.mu.Unlock()
	return nil
}
func (d *DemandManager) StopDiscovery() { d.mu.Lock(); d.until = time.Time{}; d.mu.Unlock(); d.poll() }
func (d *DemandManager) Reset() {
	d.epoch.Add(1)
	d.StopDiscovery()
	for _, addr := range d.base.Connected() {
		_ = d.base.Disconnect(addr)
	}
	d.poll()
}
func (d *DemandManager) SetCallbacks(n func(string, uint16, []byte, bool), c func(string, byte)) {
	d.base.SetCallbacks(n, c)
}
func (d *DemandManager) Connected() []string { return d.base.Connected() }
func (d *DemandManager) Slots() (int, int) {
	free, limit := d.base.Slots()
	if !d.powered.Load() {
		return limit, limit
	}
	return free, limit
}
func (d *DemandManager) Connect(a string, t int) (ConnInfo, error) {
	epoch := d.epoch.Load()
	if e := d.begin(); e != nil {
		return ConnInfo{}, e
	}
	defer d.end()
	result, err := d.base.Connect(a, t)
	if d.epoch.Load() != epoch || d.closed.Load() {
		_ = d.base.Disconnect(a)
		return ConnInfo{}, ErrDisconnected
	}
	return result, err
}
func (d *DemandManager) Disconnect(a string) error          { return d.base.Disconnect(a) }
func (d *DemandManager) Pair(a, p string) (PairInfo, error) { return d.base.Pair(a, p) }
func (d *DemandManager) ForgetBond(a string) error {
	if e := d.begin(); e != nil {
		return e
	}
	defer d.end()
	return d.base.ForgetBond(a)
}
func (d *DemandManager) Services(a string) ([]Service, error)    { return d.base.Services(a) }
func (d *DemandManager) Read(a string, h uint16) ([]byte, error) { return d.base.Read(a, h) }
func (d *DemandManager) Write(a string, h uint16, v []byte, r bool) error {
	return d.base.Write(a, h, v, r)
}

func (d *DemandManager) Close() {
	d.closeOnce.Do(func() {
		d.closed.Store(true)
		close(d.stop)
		d.Reset()
		// Closing a raw controller can synchronously report disconnections.
		// Those callbacks read RadioStatus, so do not hold the lifecycle mutex.
		// closed prevents new operations from powering the adapter back up.
		_ = d.power(false)
	})
}
func (d *DemandManager) RadioStatus() (bool, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.powered.Load(), time.Now().Before(d.until) && !d.busy.Load()
}
