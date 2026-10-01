package auth

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }

func newStore(t *testing.T) (*Store, *fakeClock) {
	s, err := Open(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	c := &fakeClock{t: time.Date(2026, 10, 1, 14, 0, 5, 0, time.UTC)}
	s.Now = c.now
	return s, c
}

func digits(c string) string { return strings.ReplaceAll(c, " ", "") }

// wrongCode returns a 6-digit code that is neither of the live codes.
func wrongCode(s *Store) string {
	a, u, _ := s.CurrentCodes()
	for _, w := range []string{"000000", "111111", "222222"} {
		if w != digits(a) && w != digits(u) {
			return w
		}
	}
	return "333333"
}

func TestTwoRollingCodes(t *testing.T) {
	s, clk := newStore(t)
	a, u, exp := s.CurrentCodes()
	if len(a) != 7 || a[3] != ' ' || len(u) != 7 {
		t.Fatalf("format %q %q", a, u)
	}
	if a == u {
		t.Fatal("admin and user codes are equal")
	}
	if !exp.Equal(time.Date(2026, 10, 1, 14, 1, 0, 0, time.UTC)) {
		t.Fatalf("expiry not aligned to the minute: %v", exp)
	}
	if a2, u2, _ := s.CurrentCodes(); a2 != a || u2 != u {
		t.Fatal("codes changed within the same minute")
	}
	changed := false
	for i := 0; i < 3 && !changed; i++ {
		clk.add(time.Minute)
		a2, u2, _ := s.CurrentCodes()
		changed = a2 != a && u2 != u
	}
	if !changed {
		t.Fatal("codes did not roll")
	}
}

func TestRoleFromCode(t *testing.T) {
	s, _ := newStore(t)
	a, u, _ := s.CurrentCodes()
	_, dev, err := s.Redeem(a, "Admin PC", "1.1.1.1", "Funnel")
	if err != nil || dev.Role != RoleAdmin {
		t.Fatalf("admin redeem: %v %+v", err, dev)
	}
	// The user code is untouched by the admin sign-in.
	if _, u2, _ := s.CurrentCodes(); u2 != u {
		t.Fatal("user code changed when the admin code was used")
	}
	_, dev, err = s.Redeem(u, "Guest phone", "2.2.2.2", "Funnel")
	if err != nil || dev.Role != RoleUser {
		t.Fatalf("user redeem: %v %+v", err, dev)
	}
	// Both are single use.
	if _, _, err := s.Redeem(a, "x", "3.3.3.3", "Funnel"); err == nil {
		t.Fatal("admin code reused")
	}
	if _, _, err := s.Redeem(u, "x", "3.3.3.3", "Funnel"); err == nil {
		t.Fatal("user code reused")
	}
}

// TestCodesNeverCollide forces the generator to repeat itself and checks, at
// every step and through every kind of roll, that no two simultaneously
// accepted codes are equal.
func TestCodesNeverCollide(t *testing.T) {
	s, clk := newStore(t)
	rng := rand.New(rand.NewSource(1))
	pool := []string{"111111", "222222", "333333", "444444", "555555"} // tiny pool => constant collisions
	s.gen = func() string { return pool[rng.Intn(len(pool))] }

	check := func(step string) {
		t.Helper()
		seen := map[string]Role{}
		for _, r := range roles {
			sl := s.codes[r]
			for _, c := range []*loginCode{sl.cur, sl.prev} {
				if c == nil {
					continue
				}
				if other, dup := seen[c.code]; dup {
					t.Fatalf("%s: code %s live for both %s and %s", step, c.code, other, r)
				}
				seen[c.code] = r
			}
		}
	}
	for i := 0; i < 3000; i++ {
		switch rng.Intn(4) {
		case 0: // time passes, sometimes across a minute boundary, sometimes inside grace
			clk.add(time.Duration(rng.Intn(70)) * time.Second)
			s.CurrentCodes()
		case 1: // someone signs in with the admin or user code (current or grace)
			s.mu.Lock()
			s.refreshLocked(s.Now())
			r := roles[rng.Intn(2)]
			sl := s.codes[r]
			code := sl.cur.code
			if sl.prev != nil && rng.Intn(2) == 0 {
				code = sl.prev.code
			}
			s.mu.Unlock()
			if _, dev, err := s.Redeem(code, "x", fmt.Sprintf("10.%d.0.1", i), "Funnel"); err != nil || dev.Role != r {
				t.Fatalf("step %d: redeem %s code got role %s: %v", i, r, dev.Role, err)
			}
		case 2: // wrong guesses (can trigger an early roll of both)
			s.Redeem("999999", "x", fmt.Sprintf("11.%d.0.1", i), "Funnel")
		case 3:
			s.CurrentCodes()
		}
		s.mu.Lock()
		check(fmt.Sprintf("step %d", i))
		s.mu.Unlock()
		// keep lockouts from hiding the behaviour under test
		s.ipFails, s.globalFails = map[string][]time.Time{}, nil
	}
}

func TestGraceAndSingleUse(t *testing.T) {
	s, clk := newStore(t)
	oldA, oldU, _ := s.CurrentCodes()
	clk.add(59 * time.Second) // 14:01:04 — rolled 4s ago
	s.CurrentCodes()
	if _, dev, err := s.Redeem(oldA, "A", "1.1.1.1", "Funnel"); err != nil || dev.Role != RoleAdmin {
		t.Fatalf("previous admin code within grace: %v %+v", err, dev)
	}
	if _, _, err := s.Redeem(oldA, "B", "1.1.1.1", "Funnel"); err == nil {
		t.Fatal("code reused")
	}
	if _, dev, err := s.Redeem(oldU, "U", "1.1.1.1", "Funnel"); err != nil || dev.Role != RoleUser {
		t.Fatalf("previous user code within grace: %v %+v", err, dev)
	}

	s2, clk2 := newStore(t)
	oldA, _, _ = s2.CurrentCodes()
	clk2.add(55*time.Second + codeGrace + time.Second)
	if _, _, err := s2.Redeem(oldA, "C", "2.2.2.2", "LAN"); err == nil {
		t.Fatal("expired code accepted after grace")
	}
}

func TestSessionLifetime(t *testing.T) {
	s, clk := newStore(t)
	var events []string
	s.OnEvent = func(k, _ string) { events = append(events, k) }
	_, u, _ := s.CurrentCodes()
	tok, dev, err := s.Redeem(" "+u+" ", "Chrome", "1.1.1.1", "Funnel")
	if err != nil || tok == "" || dev.TokenHash != "" || dev.Role != RoleUser {
		t.Fatalf("redeem: %v %+v", err, dev)
	}
	if !dev.Expires.Equal(clk.t.Add(SessionTTL)) {
		t.Fatalf("expires %v", dev.Expires)
	}
	clk.add(23 * time.Hour)
	if d, ok := s.Check(tok, "2.2.2.2", "Tailnet"); !ok || d.LastIP != "2.2.2.2" || d.Role != RoleUser {
		t.Fatal("session should still be valid at 23h")
	}
	clk.add(time.Hour)
	if _, ok := s.Check(tok, "", ""); ok {
		t.Fatal("session outlived 24h")
	}
	if len(s.List()) != 0 {
		t.Fatal("expired session still listed")
	}
	if strings.Join(events, ",") != "login" {
		t.Fatalf("events %v", events)
	}
}

func TestWrongTriesRollBothCodes(t *testing.T) {
	s, _ := newStore(t)
	var events []string
	s.OnEvent = func(k, _ string) { events = append(events, k) }
	a, u, _ := s.CurrentCodes()
	w := wrongCode(s)
	for i := 1; i <= 4; i++ {
		var we WrongCodeError
		if _, _, err := s.Redeem(w, "", fmt.Sprintf("9.9.9.%d", i), "Funnel"); !errors.As(err, &we) || we.Left != 5-i {
			t.Fatalf("try %d: %v", i, err)
		}
	}
	if _, _, err := s.Redeem(w, "", "9.9.9.5", "Funnel"); !errors.Is(err, ErrNoCode) {
		t.Fatalf("5th try: %v", err)
	}
	a2, u2, _ := s.CurrentCodes()
	if a2 == a || u2 == u {
		t.Fatal("codes did not roll after 5 wrong tries")
	}
	for _, old := range []string{a, u} {
		if _, _, err := s.Redeem(old, "", "8.8.8.8", "Funnel"); err == nil {
			t.Fatal("rolled-away code still accepted")
		}
	}
	if strings.Join(events, ",") != "code_rolled" {
		t.Fatalf("events %v", events)
	}
}

func TestLockouts(t *testing.T) {
	s, clk := newStore(t)
	for i := 0; i < ipMaxFailures; i++ {
		s.Redeem("x", "", "5.5.5.5", "Funnel")
	}
	a, _, _ := s.CurrentCodes()
	if _, _, err := s.Redeem(a, "", "5.5.5.5", "Funnel"); !errors.Is(err, ErrIPLocked) {
		t.Fatalf("ip lockout: %v", err)
	}
	for i := 0; i < globalMaxFails; i++ {
		s.Redeem("x", "", fmt.Sprintf("10.0.%d.1", i), "Funnel")
	}
	a, _, _ = s.CurrentCodes()
	if _, _, err := s.Redeem(a, "", "6.6.6.6", "Funnel"); !errors.Is(err, ErrGlobalLock) {
		t.Fatalf("global lockout: %v", err)
	}
	clk.add(globalWindow + time.Second)
	a, _, _ = s.CurrentCodes()
	if _, _, err := s.Redeem(a, "", "6.6.6.6", "Funnel"); err != nil {
		t.Fatalf("global lock should lift: %v", err)
	}
}

func TestPersistRevokeAndLegacyRole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	s, _ := Open(path)
	a, _, _ := s.CurrentCodes()
	tok, dev, _ := s.Redeem(a, "X", "1.1.1.1", "LAN")
	s2, _ := Open(path)
	if d, ok := s2.Check(tok, "", ""); !ok || d.Role != RoleAdmin {
		t.Fatal("not persisted with role")
	}
	if !s2.Revoke(dev.ID) || len(s2.List()) != 0 {
		t.Fatal("revoke")
	}
	if l := s2.List(); l == nil {
		t.Fatal("List must return [] not nil")
	}

	// Sessions saved before roles existed become read-only users.
	legacy := filepath.Join(t.TempDir(), "old.json")
	writeFile(t, legacy, `[{"id":"a","name":"old","tokenHash":"`+hashToken("t")+`","created":"2099-01-01T00:00:00Z","expires":"2099-01-01T12:00:00Z"}]`)
	s3, _ := Open(legacy)
	s3.Now = func() time.Time { return time.Date(2099, 1, 1, 1, 0, 0, 0, time.UTC) }
	if d, ok := s3.Check("t", "", ""); !ok || d.Role != RoleUser {
		t.Fatalf("legacy role: %v %+v", ok, d)
	}
}

func TestRandomDigitsUniformAlphabet(t *testing.T) {
	seen := map[byte]bool{}
	for i := 0; i < 200; i++ {
		for _, c := range []byte(randomDigits(6)) {
			if c < '0' || c > '9' {
				t.Fatalf("bad char %q", c)
			}
			seen[c] = true
		}
	}
	if len(seen) != 10 {
		t.Fatalf("only %d digits seen", len(seen))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
