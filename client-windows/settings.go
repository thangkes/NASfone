//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// settings are local preferences of this Windows app (not part of pairing).
type settings struct {
	Drive string `json:"drive"` // preferred drive letter, e.g. "P"
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

func setDrive(letter string) {
	letter = strings.ToUpper(strings.TrimSuffix(strings.TrimSpace(letter), ":"))
	if len(letter) != 1 || letter[0] < 'D' || letter[0] > 'Z' {
		return
	}
	settingsMu.Lock()
	if settingsV == nil {
		settingsV = &settings{}
	}
	settingsV.Drive = letter
	b, _ := json.MarshalIndent(settingsV, "", "  ")
	settingsMu.Unlock()
	writeAtomic(settingsPath(), b)
}
