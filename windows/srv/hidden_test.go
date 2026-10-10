//go:build windows

package srv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHiddenDirsCoverProfiles(t *testing.T) {
	dirs := hiddenDirs()
	has := func(p string) bool {
		for _, d := range dirs {
			if strings.EqualFold(d, p) {
				return true
			}
		}
		return false
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		dataDir(),
		filepath.Join(home, `AppData\Roaming\NASfone-Server`),
		filepath.Join(home, `AppData\Roaming\NASfone`),
		filepath.Join(home, `AppData\Local\NASfone`),
	} {
		if !has(p) {
			t.Errorf("missing %s", p)
		}
	}
	for _, d := range dirs {
		t.Log(d)
	}
}
