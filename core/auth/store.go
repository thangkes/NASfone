// Package auth handles browser sign-in: one-time login codes issued from the
// server app, and the device sessions they create.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	CodeTTL         = 5 * time.Minute
	codeMaxTries    = 5
	rememberTTL     = 90 * 24 * time.Hour
	tempSessionTTL  = 12 * time.Hour
	ipFailWindow    = 15 * time.Minute
	ipMaxFailures   = 10
	lastSeenSaveGap = time.Minute
	codeAlphabet    = "ABCDEFGHJKMNPQRSTUVWXYZ23456789" // no 0/O, 1/I/L
)

var (
	ErrNoCode    = errors.New("Mã không hợp lệ hoặc đã hết hạn. Hãy tạo mã mới trên app.")
	ErrCodeSpent = errors.New("Sai quá nhiều lần, mã đã bị hủy. Hãy tạo mã mới trên app.")
	ErrIPLocked  = errors.New("Thử sai quá nhiều lần. Vui lòng đợi 15 phút.")
)

// WrongCodeError reports a wrong code and how many tries remain.
type WrongCodeError struct{ Left int }

func (e WrongCodeError) Error() string {
	return "Mã không đúng."
}

// Device is one signed-in browser.
type Device struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TokenHash string    `json:"tokenHash,omitempty"`
	Remember  bool      `json:"remember"`
	Via       string    `json:"via"`
	LastIP    string    `json:"lastIP"`
	Created   time.Time `json:"created"`
	LastSeen  time.Time `json:"lastSeen"`
	Expires   time.Time `json:"expires"`
}

type loginCode struct {
	code  string
	exp   time.Time
	tries int
}

// Store keeps devices on disk and the current login code in memory.
type Store struct {
	mu       sync.Mutex
	path     string
	devices  []*Device
	code     *loginCode
	failures map[string][]time.Time
	lastSave time.Time

	// OnEvent is called (outside the lock) for "login", "revoke", "code_spent".
	OnEvent func(kind, detail string)
}

// Open loads (or creates) the device store at path.
func Open(path string) (*Store, error) {
	s := &Store{path: path, failures: map[string][]time.Time{}}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &s.devices); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// NewCode replaces any current code with a fresh one.
func (s *Store) NewCode() (code string, exp time.Time) {
	raw := randomString(8, codeAlphabet)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.code = &loginCode{code: raw, exp: time.Now().Add(CodeTTL)}
	return FormatCode(raw), s.code.exp
}

// CurrentCode returns the active code, if any.
func (s *Store) CurrentCode() (code string, exp time.Time, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.code == nil || time.Now().After(s.code.exp) {
		s.code = nil
		return "", time.Time{}, false
	}
	return FormatCode(s.code.code), s.code.exp, true
}

// FormatCode renders "ABCD2345" as "ABCD-2345".
func FormatCode(c string) string {
	if len(c) != 8 {
		return c
	}
	return c[:4] + "-" + c[4:]
}

func normalizeCode(in string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(in) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Redeem exchanges a login code for a session token.
func (s *Store) Redeem(code, name, ip, via string, remember bool) (token string, dev Device, err error) {
	now := time.Now()
	var event, detail string
	defer func() {
		if event != "" && s.OnEvent != nil {
			s.OnEvent(event, detail)
		}
	}()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ipLocked(ip, now) {
		return "", Device{}, ErrIPLocked
	}
	if s.code == nil || now.After(s.code.exp) {
		s.code = nil
		s.recordFailure(ip, now)
		return "", Device{}, ErrNoCode
	}
	if subtle.ConstantTimeCompare([]byte(normalizeCode(code)), []byte(s.code.code)) != 1 {
		s.recordFailure(ip, now)
		s.code.tries++
		if s.code.tries >= codeMaxTries {
			s.code = nil
			event, detail = "code_spent", ip
			return "", Device{}, ErrCodeSpent
		}
		return "", Device{}, WrongCodeError{Left: codeMaxTries - s.code.tries}
	}
	s.code = nil // single use

	name = strings.TrimSpace(name)
	if name == "" {
		name = "Trình duyệt"
	}
	if len([]rune(name)) > 60 {
		name = string([]rune(name)[:60])
	}
	token = randomToken()
	d := &Device{
		ID:        randomString(10, "abcdefghijkmnpqrstuvwxyz23456789"),
		Name:      name,
		TokenHash: hashToken(token),
		Remember:  remember,
		Via:       via,
		LastIP:    ip,
		Created:   now,
		LastSeen:  now,
	}
	if remember {
		d.Expires = now.Add(rememberTTL)
	} else {
		d.Expires = now.Add(tempSessionTTL)
	}
	s.devices = append(s.devices, d)
	s.saveLocked()
	event, detail = "login", name+" ("+via+")"
	return token, d.public(), nil
}

// Check validates a session token and refreshes its last-seen time.
func (s *Store) Check(token, ip, via string) (Device, bool) {
	if token == "" {
		return Device{}, false
	}
	h := hashToken(token)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.devices {
		if subtle.ConstantTimeCompare([]byte(d.TokenHash), []byte(h)) != 1 {
			continue
		}
		if now.After(d.Expires) {
			return Device{}, false
		}
		d.LastSeen, d.LastIP, d.Via = now, ip, via
		if d.Remember {
			d.Expires = now.Add(rememberTTL) // sliding
		}
		if now.Sub(s.lastSave) > lastSeenSaveGap {
			s.saveLocked()
		}
		return d.public(), true
	}
	return Device{}, false
}

// Logout removes the session that owns token.
func (s *Store) Logout(token string) {
	h := hashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range s.devices {
		if d.TokenHash == h {
			s.devices = append(s.devices[:i], s.devices[i+1:]...)
			s.saveLocked()
			return
		}
	}
}

// Revoke removes one device by ID.
func (s *Store) Revoke(id string) bool {
	s.mu.Lock()
	var name string
	for i, d := range s.devices {
		if d.ID == id {
			name = d.Name
			s.devices = append(s.devices[:i], s.devices[i+1:]...)
			s.saveLocked()
			break
		}
	}
	s.mu.Unlock()
	if name != "" && s.OnEvent != nil {
		s.OnEvent("revoke", name)
	}
	return name != ""
}

// RevokeAll removes every device.
func (s *Store) RevokeAll() {
	s.mu.Lock()
	s.devices = nil
	s.saveLocked()
	s.mu.Unlock()
	if s.OnEvent != nil {
		s.OnEvent("revoke", "tất cả thiết bị")
	}
}

// List returns active devices, newest first, without token hashes.
func (s *Store) List() []Device {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.devices[:0]
	var out []Device
	for _, d := range s.devices {
		if now.After(d.Expires) {
			continue
		}
		kept = append(kept, d)
		out = append(out, d.public())
	}
	if len(kept) != len(s.devices) {
		s.devices = kept
		s.saveLocked()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out
}

func (d *Device) public() Device {
	c := *d
	c.TokenHash = ""
	return c
}

func (s *Store) ipLocked(ip string, now time.Time) bool {
	recent := s.failures[ip][:0]
	for _, t := range s.failures[ip] {
		if now.Sub(t) < ipFailWindow {
			recent = append(recent, t)
		}
	}
	s.failures[ip] = recent
	return len(recent) >= ipMaxFailures
}

func (s *Store) recordFailure(ip string, now time.Time) {
	s.failures[ip] = append(s.failures[ip], now)
}

// saveLocked writes the device list atomically. Callers hold s.mu.
func (s *Store) saveLocked() {
	s.lastSave = time.Now()
	b, err := json.MarshalIndent(s.devices, "", "  ")
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	os.Rename(tmp, s.path)
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// randomString draws n characters uniformly from alphabet (rejection sampling,
// so there is no modulo bias).
func randomString(n int, alphabet string) string {
	limit := 256 - 256%len(alphabet)
	out := make([]byte, 0, n)
	buf := make([]byte, 2*n)
	for len(out) < n {
		rand.Read(buf)
		for _, c := range buf {
			if int(c) < limit && len(out) < n {
				out = append(out, alphabet[int(c)%len(alphabet)])
			}
		}
	}
	return string(out)
}
