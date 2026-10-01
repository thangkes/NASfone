package auth

import (
	"errors"
	"fmt"
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

func TestRollingCode(t *testing.T) {
	s, clk := newStore(t)
	c1, exp := s.CurrentCode()
	if len(c1) != 7 || c1[3] != ' ' || len(digits(c1)) != 6 {
		t.Fatalf("format %q", c1)
	}
	if !exp.Equal(time.Date(2026, 10, 1, 14, 1, 0, 0, time.UTC)) {
		t.Fatalf("expiry not aligned to the minute: %v", exp)
	}
	if c, _ := s.CurrentCode(); c != c1 {
		t.Fatal("code changed within the same minute")
	}
	// Over several minutes the code must change (a repeat is a 1-in-a-million fluke).
	changed := false
	for i := 0; i < 3 && !changed; i++ {
		clk.add(time.Minute)
		c, _ := s.CurrentCode()
		changed = c != c1
	}
	if !changed {
		t.Fatal("code did not roll")
	}
}

func TestGraceAndSingleUse(t *testing.T) {
	s, clk := newStore(t)
	old, _ := s.CurrentCode()
	clk.add(59 * time.Second) // 14:01:04 — rolled 4s ago
	s.CurrentCode()
	if _, _, err := s.Redeem(old, "A", "1.1.1.1", "Funnel"); err != nil {
		t.Fatalf("previous code within grace rejected: %v", err)
	}
	if _, _, err := s.Redeem(old, "B", "1.1.1.1", "Funnel"); err == nil {
		t.Fatal("code reused")
	}

	s2, clk2 := newStore(t)
	old, _ = s2.CurrentCode()
	clk2.add(55*time.Second + codeGrace + time.Second) // past the grace period
	if _, _, err := s2.Redeem(old, "C", "2.2.2.2", "LAN"); err == nil {
		t.Fatal("expired code accepted after grace")
	}
}

func TestSessionLifetime(t *testing.T) {
	s, clk := newStore(t)
	var events []string
	s.OnEvent = func(k, _ string) { events = append(events, k) }
	c, _ := s.CurrentCode()
	tok, dev, err := s.Redeem(" "+c+" ", "Chrome", "1.1.1.1", "Funnel")
	if err != nil || tok == "" || dev.TokenHash != "" {
		t.Fatalf("redeem: %v %+v", err, dev)
	}
	if !dev.Expires.Equal(clk.t.Add(SessionTTL)) {
		t.Fatalf("expires %v", dev.Expires)
	}
	clk.add(23 * time.Hour)
	if d, ok := s.Check(tok, "2.2.2.2", "Tailnet"); !ok || d.LastIP != "2.2.2.2" {
		t.Fatal("session should still be valid at 23h")
	}
	clk.add(time.Hour) // 24h after sign-in; no sliding extension
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

func TestWrongTriesRollCode(t *testing.T) {
	s, _ := newStore(t)
	var events []string
	s.OnEvent = func(k, _ string) { events = append(events, k) }
	c, _ := s.CurrentCode()
	wrong := "000000"
	if digits(c) == wrong {
		wrong = "111111"
	}
	for i := 1; i <= 4; i++ {
		var w WrongCodeError
		if _, _, err := s.Redeem(wrong, "", fmt.Sprintf("9.9.9.%d", i), "Funnel"); !errors.As(err, &w) || w.Left != 5-i {
			t.Fatalf("try %d: %v", i, err)
		}
	}
	if _, _, err := s.Redeem(wrong, "", "9.9.9.5", "Funnel"); !errors.Is(err, ErrNoCode) {
		t.Fatalf("5th try: %v", err)
	}
	if now, _ := s.CurrentCode(); now == c {
		t.Fatal("code did not roll after 5 wrong tries")
	}
	if _, _, err := s.Redeem(c, "", "8.8.8.8", "Funnel"); err == nil {
		t.Fatal("rolled-away code still accepted")
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
	c, _ := s.CurrentCode()
	if _, _, err := s.Redeem(c, "", "5.5.5.5", "Funnel"); !errors.Is(err, ErrIPLocked) {
		t.Fatalf("ip lockout: %v", err)
	}
	// Many IPs together trip the global limit, which blocks everyone.
	for i := 0; i < globalMaxFails; i++ {
		s.Redeem("x", "", fmt.Sprintf("10.0.%d.1", i), "Funnel")
	}
	c, _ = s.CurrentCode()
	if _, _, err := s.Redeem(c, "", "6.6.6.6", "Funnel"); !errors.Is(err, ErrGlobalLock) {
		t.Fatalf("global lockout: %v", err)
	}
	clk.add(globalWindow + time.Second)
	c, _ = s.CurrentCode()
	if _, _, err := s.Redeem(c, "", "6.6.6.6", "Funnel"); err != nil {
		t.Fatalf("global lock should lift: %v", err)
	}
}

func TestPersistRevoke(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	s, _ := Open(path)
	c, _ := s.CurrentCode()
	tok, dev, _ := s.Redeem(c, "X", "1.1.1.1", "LAN")
	s2, _ := Open(path)
	if _, ok := s2.Check(tok, "", ""); !ok {
		t.Fatal("not persisted")
	}
	if !s2.Revoke(dev.ID) || len(s2.List()) != 0 {
		t.Fatal("revoke")
	}
	if l := s2.List(); l == nil {
		t.Fatal("List must return [] not nil")
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
