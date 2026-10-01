// Package auth handles browser sign-in: rolling 6-digit login codes shown in
// the server app, and the browser sessions they create.
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
	CodeStep        = time.Minute      // a new code every minute, aligned to the wall clock
	codeGrace       = 10 * time.Second // previous code still accepted right after it rolls
	codeDigits      = 6
	codeMaxTries    = 5 // wrong tries before the current code rolls early
	SessionTTL      = 24 * time.Hour
	ipFailWindow    = 15 * time.Minute
	ipMaxFailures   = 10
	globalWindow    = 5 * time.Minute
	globalMaxFails  = 20 // across all IPs; then sign-in pauses for globalWindow
	lastSeenSaveGap = time.Minute
)

var (
	ErrNoCode     = errors.New("Mã không đúng hoặc đã hết hạn. Hãy nhập mã đang hiện trên app.")
	ErrIPLocked   = errors.New("Thử sai quá nhiều lần. Vui lòng đợi 15 phút.")
	ErrGlobalLock = errors.New("Đăng nhập tạm khóa do có quá nhiều lần thử sai. Vui lòng đợi vài phút.")
)

// WrongCodeError reports a wrong code and how many tries remain on the current code.
type WrongCodeError struct{ Left int }

func (e WrongCodeError) Error() string { return "Mã không đúng." }

// Device is one signed-in browser session.
type Device struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TokenHash string    `json:"tokenHash,omitempty"`
	Via       string    `json:"via"`
	LastIP    string    `json:"lastIP"`
	Created   time.Time `json:"created"`
	LastSeen  time.Time `json:"lastSeen"`
	Expires   time.Time `json:"expires"`
}

type loginCode struct {
	code  string
	exp   time.Time // end of its minute
	tries int
}

// Store keeps sessions on disk and the rolling login code in memory.
type Store struct {
	mu          sync.Mutex
	path        string
	devices     []*Device
	cur, prev   *loginCode
	ipFails     map[string][]time.Time
	globalFails []time.Time
	lastSave    time.Time

	// Now is the clock; replaceable in tests.
	Now func() time.Time
	// OnEvent is called (outside the lock) for "login", "revoke", "code_rolled".
	OnEvent func(kind, detail string)
}

// Open loads (or creates) the session store at path.
func Open(path string) (*Store, error) {
	s := &Store{path: path, ipFails: map[string][]time.Time{}, Now: time.Now}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &s.devices); err != nil {
			return nil, err
		}
		// Sessions from older builds could last 90 days; cap them to the current policy.
		for _, d := range s.devices {
			if max := d.Created.Add(SessionTTL); d.Expires.After(max) {
				d.Expires = max
			}
		}
	}
	return s, nil
}

// CurrentCode returns the code for the current minute (rolling it if needed)
// formatted as "123 456", and when it expires.
func (s *Store) CurrentCode() (code string, exp time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.currentLocked(s.Now())
	return FormatCode(c.code), c.exp
}

func (s *Store) currentLocked(now time.Time) *loginCode {
	if s.cur == nil || !now.Before(s.cur.exp) {
		if s.cur != nil && now.Sub(s.cur.exp) < codeGrace {
			s.prev = s.cur
		} else {
			s.prev = nil
		}
		s.cur = &loginCode{code: randomDigits(codeDigits), exp: now.Truncate(CodeStep).Add(CodeStep)}
	}
	return s.cur
}

// FormatCode renders "123456" as "123 456".
func FormatCode(c string) string {
	if len(c) != codeDigits {
		return c
	}
	return c[:3] + " " + c[3:]
}

func normalizeCode(in string) string {
	var b strings.Builder
	for _, r := range in {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func codeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// Redeem exchanges the current login code for a session token valid for SessionTTL.
func (s *Store) Redeem(code, name, ip, via string) (token string, dev Device, err error) {
	var event, detail string
	defer func() {
		if event != "" && s.OnEvent != nil {
			s.OnEvent(event, detail)
		}
	}()

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()

	if s.globalLocked(now) {
		return "", Device{}, ErrGlobalLock
	}
	if s.ipLocked(ip, now) {
		return "", Device{}, ErrIPLocked
	}
	cur := s.currentLocked(now)
	in := normalizeCode(code)
	ok := codeEqual(in, cur.code)
	if !ok && s.prev != nil && now.Sub(s.prev.exp) < codeGrace {
		ok = codeEqual(in, s.prev.code)
	}
	if !ok {
		s.recordFailure(ip, now)
		cur.tries++
		if cur.tries >= codeMaxTries {
			// Roll early so guessing has to start over against a fresh code.
			s.cur, s.prev = nil, nil
			event, detail = "code_rolled", ip
			return "", Device{}, ErrNoCode
		}
		return "", Device{}, WrongCodeError{Left: codeMaxTries - cur.tries}
	}
	// Single use: a code that signed someone in is retired at once.
	s.cur, s.prev = nil, nil

	name = strings.TrimSpace(name)
	if name == "" {
		name = "Trình duyệt"
	}
	if r := []rune(name); len(r) > 60 {
		name = string(r[:60])
	}
	token = randomToken()
	d := &Device{
		ID:        randomString(10, "abcdefghijkmnpqrstuvwxyz23456789"),
		Name:      name,
		TokenHash: hashToken(token),
		Via:       via,
		LastIP:    ip,
		Created:   now,
		LastSeen:  now,
		Expires:   now.Add(SessionTTL),
	}
	s.devices = append(s.devices, d)
	s.saveLocked()
	event, detail = "login", name+" ("+via+")"
	return token, d.public(), nil
}

// Check validates a session token and records last-seen info. Sessions are not
// extended: they end SessionTTL after sign-in at the latest.
func (s *Store) Check(token, ip, via string) (Device, bool) {
	if token == "" {
		return Device{}, false
	}
	h := hashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	for _, d := range s.devices {
		if !codeEqual(d.TokenHash, h) {
			continue
		}
		if !now.Before(d.Expires) {
			return Device{}, false
		}
		d.LastSeen, d.LastIP, d.Via = now, ip, via
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

// Revoke removes one session by ID.
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

// RevokeAll removes every session.
func (s *Store) RevokeAll() {
	s.mu.Lock()
	s.devices = nil
	s.saveLocked()
	s.mu.Unlock()
	if s.OnEvent != nil {
		s.OnEvent("revoke", "tất cả thiết bị")
	}
}

// List returns active sessions, most recently seen first, without token hashes.
func (s *Store) List() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	kept := s.devices[:0]
	out := []Device{} // encode as [] rather than null
	for _, d := range s.devices {
		if !now.Before(d.Expires) {
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

func prune(ts []time.Time, now time.Time, window time.Duration) []time.Time {
	kept := ts[:0]
	for _, t := range ts {
		if now.Sub(t) < window {
			kept = append(kept, t)
		}
	}
	return kept
}

func (s *Store) ipLocked(ip string, now time.Time) bool {
	s.ipFails[ip] = prune(s.ipFails[ip], now, ipFailWindow)
	return len(s.ipFails[ip]) >= ipMaxFailures
}

func (s *Store) globalLocked(now time.Time) bool {
	s.globalFails = prune(s.globalFails, now, globalWindow)
	return len(s.globalFails) >= globalMaxFails
}

func (s *Store) recordFailure(ip string, now time.Time) {
	s.ipFails[ip] = append(s.ipFails[ip], now)
	s.globalFails = append(s.globalFails, now)
}

// saveLocked writes the session list atomically. Callers hold s.mu.
func (s *Store) saveLocked() {
	s.lastSave = s.Now()
	b, err := json.MarshalIndent(s.devices, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return
	}
	tmp := s.path + ".tmp"
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

// randomDigits returns n uniformly random decimal digits.
func randomDigits(n int) string {
	return randomString(n, "0123456789")
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
