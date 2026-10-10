//go:build windows

package srv

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// NASfone's own data folders inside a Windows user profile. They hold private
// keys, sign-in lists and cached copies of other servers' files, so the
// server keeps them out of the share even when the shared folder contains a
// profile (for example C:\Users\<name> or C:\Users). Only these folders are
// hidden; everything else in the profile is shared as configured.
var profileDataDirs = []string{
	`AppData\Roaming\NASfone-Server`, // this server: identity key, paired apps, sessions
	`AppData\Roaming\NASfone`,        // client role: per-server keys, role.txt
	`AppData\Local\NASfone`,          // client role: cache of files from other servers
}

// hiddenDirs lists those folders for every user profile on this computer.
// Profile locations come from the registry (ProfileList), so profiles kept
// outside C:\Users or on another drive are found too; the folders of the
// Windows user running the server come from its real (possibly redirected)
// APPDATA and LOCALAPPDATA.
func hiddenDirs() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || !filepath.IsAbs(p) {
			return
		}
		p = filepath.Clean(p)
		if k := strings.ToLower(p); !seen[k] {
			seen[k] = true
			out = append(out, p)
		}
	}

	add(dataDir())
	if base, err := os.UserConfigDir(); err == nil {
		add(filepath.Join(base, "NASfone"))
	}
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		add(filepath.Join(base, "NASfone"))
	}

	for _, profile := range userProfiles() {
		for _, rel := range profileDataDirs {
			add(filepath.Join(profile, rel))
		}
	}
	return out
}

// userProfiles returns the folder of every user profile: those listed in
// HKLM\...\ProfileList plus every folder next to the current user's profile
// (in case the registry cannot be read).
func userProfiles() []string {
	var out []string
	const listKey = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList`
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, listKey, registry.ENUMERATE_SUB_KEYS); err == nil {
		sids, _ := k.ReadSubKeyNames(-1)
		k.Close()
		for _, sid := range sids {
			sk, err := registry.OpenKey(registry.LOCAL_MACHINE, listKey+`\`+sid, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			p, _, err := sk.GetStringValue("ProfileImagePath")
			sk.Close()
			if err != nil {
				continue
			}
			if exp, err := registry.ExpandString(p); err == nil {
				p = exp
			}
			out = append(out, p)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, home)
		if des, err := os.ReadDir(filepath.Dir(home)); err == nil {
			for _, de := range des {
				if de.IsDir() {
					out = append(out, filepath.Join(filepath.Dir(home), de.Name()))
				}
			}
		}
	}
	return out
}
