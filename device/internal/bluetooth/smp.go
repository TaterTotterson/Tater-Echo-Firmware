package bluetooth

// Minimal LE Security Manager support for a bonded, six-digit passkey link.
// The MT6627 is a Bluetooth 4.0 controller, so this intentionally implements
// authenticated Legacy Pairing (KeyboardOnly central, DisplayOnly radio), not
// LE Secure Connections. Meshtastic relies on BLE pairing/bonding to protect
// its otherwise plaintext PhoneAPI link.

import (
	"crypto/aes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	smpPairRequest   = 0x01
	smpPairResponse  = 0x02
	smpPairConfirm   = 0x03
	smpPairRandom    = 0x04
	smpPairFailed    = 0x05
	smpEncryptInfo   = 0x06
	smpCentralID     = 0x07
	smpIdentityInfo  = 0x08
	smpIdentityAddr  = 0x09
	smpSecurityReq   = 0x0B
	smpIOKeyboard    = 0x02
	smpAuthBond      = 0x01
	smpAuthMITM      = 0x04
	smpKeyEnc        = 0x01
	smpKeyIdentity   = 0x02
	smpTimeout       = 30 * time.Second
	evtEncryption    = 0x08
	opLEStartEncrypt = 0x08<<10 | 0x0019
)

var (
	errPairTimeout = errors.New("ble: pairing timed out")
	errPairPIN     = errors.New("ble: PIN must be exactly six digits")
	sixDigitPIN    = regexp.MustCompile(`^[0-9]{6}$`)
)

type encryptionChange struct {
	status  byte
	enabled bool
}

type bondRecord struct {
	Address       string `json:"address"`
	AddressType   int    `json:"address_type"`
	LTK           string `json:"ltk"`
	EDIV          uint16 `json:"ediv"`
	Rand          string `json:"rand"`
	Authenticated bool   `json:"authenticated"`
	IRK           string `json:"irk,omitempty"`
	IdentityAddr  string `json:"identity_address,omitempty"`
	IdentityType  int    `json:"identity_address_type,omitempty"`
}

type bondFile struct {
	Version int                   `json:"version"`
	Bonds   map[string]bondRecord `json:"bonds"`
}

// PairInfo is safe to return to Tater. It deliberately contains no PIN or
// key material; keys remain on the satellite with mode 0600.
type PairInfo struct {
	Address       string `json:"address"`
	Bonded        bool   `json:"bonded"`
	Encrypted     bool   `json:"encrypted"`
	Authenticated bool   `json:"authenticated"`
}

func reverse16(in []byte) []byte {
	out := make([]byte, 16)
	for i := 0; i < 16; i++ {
		out[15-i] = in[i]
	}
	return out
}

// smpE implements the Security Manager's little-endian AES convention.
func smpE(key, plaintext []byte) ([16]byte, error) {
	var result [16]byte
	if len(key) != 16 || len(plaintext) != 16 {
		return result, errors.New("ble: invalid SMP AES input")
	}
	block, err := aes.NewCipher(reverse16(key))
	if err != nil {
		return result, err
	}
	out := make([]byte, 16)
	block.Encrypt(out, reverse16(plaintext))
	for i := range result {
		result[i] = out[15-i]
	}
	return result, nil
}

func smpC1(tk, random, pres, preq []byte, initiatorType byte, initiatorAddr []byte, responderType byte, responderAddr []byte) ([16]byte, error) {
	var zero [16]byte
	if len(tk) != 16 || len(random) != 16 || len(pres) != 7 || len(preq) != 7 || len(initiatorAddr) != 6 || len(responderAddr) != 6 {
		return zero, errors.New("ble: invalid SMP confirm input")
	}
	p1 := make([]byte, 16)
	p1[0], p1[1] = initiatorType, responderType
	copy(p1[2:9], preq)
	copy(p1[9:16], pres)
	p2 := make([]byte, 16)
	copy(p2[0:6], responderAddr)
	copy(p2[6:12], initiatorAddr)
	x := make([]byte, 16)
	for i := range x {
		x[i] = random[i] ^ p1[i]
	}
	one, err := smpE(tk, x)
	if err != nil {
		return zero, err
	}
	for i := range x {
		x[i] = one[i] ^ p2[i]
	}
	return smpE(tk, x)
}

func smpS1(tk, responderRandom, initiatorRandom []byte) ([16]byte, error) {
	var input [16]byte
	if len(tk) != 16 || len(responderRandom) != 16 || len(initiatorRandom) != 16 {
		return input, errors.New("ble: invalid SMP key input")
	}
	copy(input[0:8], initiatorRandom[0:8])
	copy(input[8:16], responderRandom[0:8])
	return smpE(tk, input[:])
}

func pairingTK(pin string) ([]byte, error) {
	if !sixDigitPIN.MatchString(pin) {
		return nil, errPairPIN
	}
	var value uint32
	for _, digit := range pin {
		value = value*10 + uint32(digit-'0')
	}
	tk := make([]byte, 16)
	binary.LittleEndian.PutUint32(tk, value)
	return tk, nil
}

func startEncryptionParams(handle uint16, random []byte, ediv uint16, key []byte) ([]byte, error) {
	if len(random) != 8 || len(key) != 16 {
		return nil, errors.New("ble: invalid encryption material")
	}
	p := make([]byte, 28)
	binary.LittleEndian.PutUint16(p[0:2], handle)
	copy(p[2:10], random)
	binary.LittleEndian.PutUint16(p[10:12], ediv)
	copy(p[12:28], key)
	return p, nil
}

func decodeHexSized(value string, size int) ([]byte, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != size {
		return nil, errors.New("invalid stored BLE bond")
	}
	return decoded, nil
}

func loadBondFile(path string) (map[string]bondRecord, error) {
	if strings.TrimSpace(path) == "" {
		return map[string]bondRecord{}, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]bondRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	var file bondFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, err
	}
	if file.Bonds == nil {
		file.Bonds = map[string]bondRecord{}
	}
	return file.Bonds, nil
}

func saveBondFile(path string, bonds map[string]bondRecord) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(bondFile{Version: 1, Bonds: bonds}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (m *ConnManager) SetBondStore(path string) error {
	bonds, err := loadBondFile(path)
	if err != nil {
		return fmt.Errorf("ble: load bonds: %w", err)
	}
	m.mu.Lock()
	m.bondPath, m.bonds = path, bonds
	m.mu.Unlock()
	return nil
}

func (m *ConnManager) bond(addr string) (bondRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bond, ok := m.bonds[strings.ToLower(addr)]
	return bond, ok
}

func (m *ConnManager) storeBond(record bondRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.bonds == nil {
		m.bonds = map[string]bondRecord{}
	}
	key := strings.ToLower(record.Address)
	path := m.bondPath
	copyOfBonds := make(map[string]bondRecord, len(m.bonds)+1)
	for key, value := range m.bonds {
		copyOfBonds[key] = value
	}
	copyOfBonds[key] = record
	if err := saveBondFile(path, copyOfBonds); err != nil {
		return fmt.Errorf("ble: save bond: %w", err)
	}
	m.bonds = copyOfBonds
	return nil
}

// ForgetBond removes the keys for one peer. It does not silently remove a
// bond after an ordinary radio error: callers use this only for an explicit
// re-pair attempt, after ending any link encrypted with the old key.
func (m *ConnManager) ForgetBond(addr string) error {
	addr, ok := normaliseAddr(addr)
	if !ok {
		return fmt.Errorf("ble: bad address %q", addr)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.bonds[addr]; !exists {
		return nil
	}
	next := make(map[string]bondRecord, len(m.bonds)-1)
	for key, value := range m.bonds {
		if key != addr {
			next[key] = value
		}
	}
	if err := saveBondFile(m.bondPath, next); err != nil {
		return fmt.Errorf("ble: save bonds after forget: %w", err)
	}
	m.bonds = next
	return nil
}

func (m *ConnManager) waitSMP(c *leConn, deadline <-chan time.Time) ([]byte, error) {
	for {
		select {
		case pdu := <-c.smp:
			if len(pdu) == 0 || pdu[0] == smpSecurityReq {
				continue
			}
			if pdu[0] == smpPairFailed {
				reason := byte(0)
				if len(pdu) > 1 {
					reason = pdu[1]
				}
				return nil, fmt.Errorf("ble: pairing rejected (SMP 0x%02x)", reason)
			}
			return pdu, nil
		case <-c.gone:
			return nil, ErrDisconnected
		case <-deadline:
			return nil, errPairTimeout
		}
	}
}

func (m *ConnManager) waitEncryption(c *leConn, deadline <-chan time.Time) error {
	select {
	case event := <-c.enc:
		if event.status != 0 || !event.enabled {
			return fmt.Errorf("ble: encryption failed (HCI 0x%02x)", event.status)
		}
		m.mu.Lock()
		c.encrypted = true
		m.mu.Unlock()
		return nil
	case <-c.gone:
		return ErrDisconnected
	case <-deadline:
		return errPairTimeout
	}
}

func (m *ConnManager) startEncryption(c *leConn, random []byte, ediv uint16, key []byte, deadline <-chan time.Time) error {
	m.mu.Lock()
	host := m.host
	m.mu.Unlock()
	if host == nil {
		return ErrNotRunning
	}
	params, err := startEncryptionParams(c.handle, random, ediv, key)
	if err != nil {
		return err
	}
	if _, err := host.Cmd(opLEStartEncrypt, params); err != nil {
		return err
	}
	return m.waitEncryption(c, deadline)
}

func (m *ConnManager) encryptStoredBond(c *leConn, record bondRecord) error {
	key, err := decodeHexSized(record.LTK, 16)
	if err != nil {
		return err
	}
	random, err := decodeHexSized(record.Rand, 8)
	if err != nil {
		return err
	}
	timer := time.NewTimer(smpTimeout)
	defer timer.Stop()
	return m.startEncryption(c, random, record.EDIV, key, timer.C)
}

// Pair authenticates the connected peer with a six-digit passkey, enables
// encryption, and persists the responder's LTK/EDIV/Rand bond. The passkey is
// used only to derive the temporary key and is never written to disk.
func (m *ConnManager) Pair(addr, pin string) (PairInfo, error) {
	tk, err := pairingTK(pin)
	if err != nil {
		return PairInfo{}, err
	}
	c := m.byAddr(addr)
	if c == nil {
		return PairInfo{}, ErrNotConnected
	}
	c.security.Lock()
	defer c.security.Unlock()
	if record, ok := m.bond(c.addr); ok {
		m.mu.Lock()
		encrypted := c.encrypted
		m.mu.Unlock()
		if !encrypted {
			if err := m.encryptStoredBond(c, record); err != nil {
				return PairInfo{}, err
			}
		}
		return PairInfo{Address: c.addr, Bonded: true, Encrypted: true, Authenticated: record.Authenticated}, nil
	}

	preq := []byte{smpPairRequest, smpIOKeyboard, 0, smpAuthBond | smpAuthMITM, 16, 0, smpKeyEnc | smpKeyIdentity}
	m.mu.Lock()
	host := m.host
	m.mu.Unlock()
	if host == nil {
		return PairInfo{}, ErrNotRunning
	}
	if err := m.send(host, c, cidSMP, preq); err != nil {
		return PairInfo{}, err
	}
	timer := time.NewTimer(smpTimeout)
	defer timer.Stop()
	pres, err := m.waitSMP(c, timer.C)
	if err != nil {
		return PairInfo{}, err
	}
	if len(pres) != 7 || pres[0] != smpPairResponse || pres[4] != 16 || pres[3]&smpAuthMITM == 0 {
		_ = m.send(host, c, cidSMP, []byte{smpPairFailed, 0x03})
		return PairInfo{}, errors.New("ble: peer refused authenticated passkey pairing")
	}
	initiatorRandom := make([]byte, 16)
	if _, err := rand.Read(initiatorRandom); err != nil {
		return PairInfo{}, err
	}
	confirm, err := smpC1(tk, initiatorRandom, pres, preq, c.ownType, c.ownAddr[:], c.peerType, c.peerAddr[:])
	if err != nil {
		return PairInfo{}, err
	}
	if err := m.send(host, c, cidSMP, append([]byte{smpPairConfirm}, confirm[:]...)); err != nil {
		return PairInfo{}, err
	}
	peerConfirm, err := m.waitSMP(c, timer.C)
	if err != nil {
		return PairInfo{}, err
	}
	if len(peerConfirm) != 17 || peerConfirm[0] != smpPairConfirm {
		return PairInfo{}, errors.New("ble: malformed pairing confirmation")
	}
	if err := m.send(host, c, cidSMP, append([]byte{smpPairRandom}, initiatorRandom...)); err != nil {
		return PairInfo{}, err
	}
	peerRandomPDU, err := m.waitSMP(c, timer.C)
	if err != nil {
		return PairInfo{}, err
	}
	if len(peerRandomPDU) != 17 || peerRandomPDU[0] != smpPairRandom {
		return PairInfo{}, errors.New("ble: malformed pairing random")
	}
	peerRandom := peerRandomPDU[1:]
	wantConfirm, err := smpC1(tk, peerRandom, pres, preq, c.ownType, c.ownAddr[:], c.peerType, c.peerAddr[:])
	if err != nil {
		return PairInfo{}, err
	}
	if equal16(peerConfirm[1:], confirm[:]) || !equal16(peerConfirm[1:], wantConfirm[:]) {
		_ = m.send(host, c, cidSMP, []byte{smpPairFailed, 0x04})
		return PairInfo{}, errors.New("ble: PIN confirmation failed")
	}
	stk, err := smpS1(tk, peerRandom, initiatorRandom)
	if err != nil {
		return PairInfo{}, err
	}
	if err := m.startEncryption(c, make([]byte, 8), 0, stk[:], timer.C); err != nil {
		return PairInfo{}, err
	}

	var ltk, randomValue, irk []byte
	var ediv uint16
	var identityAddr string
	var identityType int
	wantKeys := pres[6] & (smpKeyEnc | smpKeyIdentity)
	for (wantKeys&smpKeyEnc != 0 && (ltk == nil || randomValue == nil)) || (wantKeys&smpKeyIdentity != 0 && (irk == nil || identityAddr == "")) {
		pdu, err := m.waitSMP(c, timer.C)
		if err != nil {
			return PairInfo{}, err
		}
		switch pdu[0] {
		case smpEncryptInfo:
			if len(pdu) == 17 {
				ltk = append([]byte(nil), pdu[1:]...)
			}
		case smpCentralID:
			if len(pdu) == 11 {
				ediv = binary.LittleEndian.Uint16(pdu[1:3])
				randomValue = append([]byte(nil), pdu[3:11]...)
			}
		case smpIdentityInfo:
			if len(pdu) == 17 {
				irk = append([]byte(nil), pdu[1:]...)
			}
		case smpIdentityAddr:
			if len(pdu) == 8 {
				identityType = int(pdu[1])
				identityAddr = formatLEAddress(pdu[2:8])
			}
		}
	}
	if len(ltk) != 16 || len(randomValue) != 8 {
		return PairInfo{}, errors.New("ble: peer encrypted the link but did not provide a bond key")
	}
	record := bondRecord{
		Address: c.addr, AddressType: int(c.peerType), LTK: hex.EncodeToString(ltk),
		EDIV: ediv, Rand: hex.EncodeToString(randomValue), Authenticated: true,
		IdentityAddr: identityAddr, IdentityType: identityType,
	}
	if len(irk) == 16 {
		record.IRK = hex.EncodeToString(irk)
	}
	if err := m.storeBond(record); err != nil {
		return PairInfo{}, err
	}
	return PairInfo{Address: c.addr, Bonded: true, Encrypted: true, Authenticated: true}, nil
}

func equal16(a, b []byte) bool {
	if len(a) != 16 || len(b) != 16 {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func formatLEAddress(raw []byte) string {
	if len(raw) != 6 {
		return ""
	}
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", raw[5], raw[4], raw[3], raw[2], raw[1], raw[0])
}
