//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// settings are local preferences of this Windows app (not part of pairing).
type settings struct {
	Drive string `json:"drive"` // drive letter of the single pairing of NASfone ≤ 0.2.0 (migrated per server)
	Lang  string `json:"lang"`  // "", "en" or "vi" ("" = follow Windows)
}

var (
	settingsMu sync.Mutex
	settingsV  *settings
)

func settingsPath() string { return filepath.Join(dataDir(), "settings.json") }

func getSettings() settings {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	if settingsV == nil {
		settingsV = &settings{Drive: "P"}
		if b, err := os.ReadFile(settingsPath()); err == nil {
			json.Unmarshal(b, settingsV)
		}
		if settingsV.Drive == "" {
			settingsV.Drive = "P"
		}
	}
	return *settingsV
}

func setLang(l string) {
	if l != "" && l != "en" && l != "vi" {
		return
	}
	settingsMu.Lock()
	if settingsV == nil {
		settingsV = &settings{Drive: "P"}
	}
	settingsV.Lang = l
	b, _ := json.MarshalIndent(settingsV, "", "  ")
	settingsMu.Unlock()
	writeAtomic(settingsPath(), b)
}
