package auth

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodeFlow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	s.OnEvent = func(k, d string) { events = append(events, k) }

	if _, _, err := s.Redeem("AAAA-AAAA", "x", "1.1.1.1", "LAN", true); !errors.Is(err, ErrNoCode) {
		t.Fatalf("no code: %v", err)
	}
	code, _ := s.NewCode()
	if len(code) != 9 || code[4] != '-' {
		t.Fatalf("format %q", code)
	}
	// lower-case, no dash, extra spaces are accepted
	tok, dev, err := s.Redeem(" "+strings.ToLower(strings.ReplaceAll(code, "-", ""))+" ", "Chrome", "1.1.1.1", "Funnel", true)
	if err != nil || tok == "" || dev.TokenHash != "" {
		t.Fatalf("redeem: %v %+v", err, dev)
	}
	if _, _, err := s.Redeem(code, "again", "1.1.1.1", "LAN", true); !errors.Is(err, ErrNoCode) {
		t.Fatal("code must be single-use")
	}
	if d, ok := s.Check(tok, "2.2.2.2", "Tailnet"); !ok || d.Name != "Chrome" || d.LastIP != "2.2.2.2" {
		t.Fatalf("check: %v %+v", ok, d)
	}
	if _, ok := s.Check("bogus", "", ""); ok {
		t.Fatal("bogus token accepted")
	}

	// persisted across reopen
	s2, _ := Open(path)
	if _, ok := s2.Check(tok, "", ""); !ok {
		t.Fatal("not persisted")
	}

	// 5 wrong tries spend the code
	code, _ = s.NewCode()
	for i := 1; i <= 4; i++ {
		var w WrongCodeError
		if _, _, err := s.Redeem("ZZZZ-ZZZZ", "", "3.3.3.3", "LAN", false); !errors.As(err, &w) || w.Left != 5-i {
			t.Fatalf("try %d: %v", i, err)
		}
	}
	if _, _, err := s.Redeem("ZZZZ-ZZZZ", "", "3.3.3.3", "LAN", false); !errors.Is(err, ErrCodeSpent) {
		t.Fatalf("5th: %v", err)
	}
	if _, _, err := s.Redeem(code, "", "4.4.4.4", "LAN", false); !errors.Is(err, ErrNoCode) {
		t.Fatal("spent code still valid")
	}

	// IP lockout after 10 failures in the window
	for i := 0; i < 10; i++ {
		s.Redeem("x", "", "5.5.5.5", "LAN", false)
	}
	s.NewCode()
	c, _, _ := s.CurrentCode()
	if _, _, err := s.Redeem(c, "", "5.5.5.5", "LAN", false); !errors.Is(err, ErrIPLocked) {
		t.Fatalf("lockout: %v", err)
	}

	// revoke
	if len(s.List()) != 1 || !s.Revoke(dev.ID) || len(s.List()) != 0 {
		t.Fatal("revoke")
	}
	if _, ok := s.Check(tok, "", ""); ok {
		t.Fatal("revoked token accepted")
	}
	if strings.Join(events, ",") != "login,code_spent,revoke" {
		t.Fatalf("events %v", events)
	}
}

func TestRandomStringAlphabet(t *testing.T) {
	seen := map[byte]bool{}
	for i := 0; i < 200; i++ {
		for _, c := range []byte(randomString(8, codeAlphabet)) {
			if !strings.ContainsRune(codeAlphabet, rune(c)) {
				t.Fatalf("bad char %q", c)
			}
			seen[c] = true
		}
	}
	if len(seen) != len(codeAlphabet) {
		t.Fatalf("only %d/%d chars seen", len(seen), len(codeAlphabet))
	}
}
