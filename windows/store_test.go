//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nasfone/core/client"
)

// TestMigrateLegacy checks that the single pairing of NASfone ≤ 0.2.0 moves
// into servers\<id>\ with its key and drive letter, and that a second server
// is listed after it.
func TestMigrateLegacy(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	settingsV = &settings{Drive: "R"}
	defer func() { settingsV = nil }()

	key, _ := client.GenerateKey()
	// Write key.bin and config.json the old way, directly in dataDir.
	if err := saveKey("abc", key); err != nil {
		t.Fatal(err)
	}
	blob, _ := os.ReadFile(keyPath("abc"))
	os.RemoveAll(serversDir())
	os.WriteFile(filepath.Join(dataDir(), "key.bin"), blob, 0o600)
	old := client.Config{URL: "https://nasfone.example.ts.net", ServerFP: "ABCDEF0123456789aa", PairedAt: time.Now().Add(-time.Hour)}
	b, _ := json.Marshal(old)
	os.WriteFile(filepath.Join(dataDir(), "config.json"), b, 0o600)

	ids := listServers()
	if len(ids) != 1 || ids[0] != "abcdef012345" {
		t.Fatalf("ids = %v", ids)
	}
	if _, err := os.Stat(filepath.Join(dataDir(), "config.json")); !os.IsNotExist(err) {
		t.Fatal("legacy config.json still there")
	}
	k2, err := loadKey(ids[0])
	if err != nil || !k2.Equal(key) {
		t.Fatalf("key after migration: %v", err)
	}
	if d := serverDrive(ids[0]); d != "R" {
		t.Fatalf("drive = %q, want R", d)
	}

	second := client.Config{URL: "https://office.example.ts.net", ServerFP: "0123456789abcdef", PairedAt: time.Now()}
	id2 := serverID(second.ServerFP)
	if err := saveConfig(id2, second); err != nil {
		t.Fatal(err)
	}
	if l := newDriveLetter(id2); l == "R" || l == "" {
		t.Fatalf("new letter %q collides with the first server", l)
	}
	if ids := listServers(); len(ids) != 2 || ids[1] != id2 {
		t.Fatalf("ids = %v", ids)
	}
	forget(ids[0])
	if ids := listServers(); len(ids) != 1 || ids[0] != id2 {
		t.Fatalf("after forget: %v", ids)
	}
}

func TestNormLetterAndID(t *testing.T) {
	for in, want := range map[string]string{"p": "P", " Q: ": "Q", "C": "", "PP": "", "": ""} {
		if got := normLetter(in); got != want {
			t.Errorf("normLetter(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "..", `a\b`, "ABC", "xyz"} {
		if validID(bad) {
			t.Errorf("validID(%q) = true", bad)
		}
	}
}
