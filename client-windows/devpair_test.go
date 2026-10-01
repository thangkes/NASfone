//go:build windows

package main

import (
	"context"
	"os"
	"testing"

	"pocketnas/core/client"
)

// TestDevPair pairs this Windows user with a dev server without the
// confirmation dialog. Manual use only:
//
//	PNAS_DEV_INVITE=pnas1:... go test -run TestDevPair .
func TestDevPair(t *testing.T) {
	inv := os.Getenv("PNAS_DEV_INVITE")
	if inv == "" {
		t.Skip("PNAS_DEV_INVITE not set")
	}
	key, _ := client.GenerateKey()
	cfg, err := client.Pair(context.Background(), httpClient, inv, key, "Windows – "+computerName()+" (dev)", "windows")
	if err != nil {
		t.Fatal(err)
	}
	if err := saveKey(key); err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	k2, err := loadKey()
	if err != nil || !k2.Equal(key) {
		t.Fatalf("DPAPI round-trip: %v", err)
	}
	t.Logf("paired as %s, role %s, data in %s", cfg.DeviceID, cfg.Role, dataDir())
}
