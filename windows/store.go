//go:build windows

package main

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"nasfone/core/client"
)

// dataDir is %APPDATA%\NASfone. Each paired server has its own folder
// servers\<id>\ with config.json (not secret), key.bin (this computer's
// private key for that server, encrypted with DPAPI for this Windows user
// only) and drive.txt (the preferred drive letter).
func dataDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	d := filepath.Join(base, "NASfone")
	os.MkdirAll(d, 0o700)
	return d
}

func serversDir() string { return filepath.Join(dataDir(), "servers") }

// serverID names a server's folder: the start of its key fingerprint, so
// pairing the same server again replaces its entry instead of adding one.
func serverID(fp string) string {
	fp = strings.ToLower(fp)
	if len(fp) > 12 {
		fp = fp[:12]
	}
	return fp
}

// validID guards ids coming from the window or the command line.
func validID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func serverDir(id string) string  { return filepath.Join(serversDir(), id) }
func configPath(id string) string { return filepath.Join(serverDir(id), "config.json") }
func keyPath(id string) string    { return filepath.Join(serverDir(id), "key.bin") }
func drivePath(id string) string  { return filepath.Join(serverDir(id), "drive.txt") }

var errNotPaired = errors.New("not paired")

var migrateOnce sync.Once

// migrateLegacy moves the single pairing of NASfone ≤ 0.2.0 (config.json and
// key.bin directly in dataDir) into servers\<id>\, keeping its drive letter.
func migrateLegacy() {
	migrateOnce.Do(func() {
		old := filepath.Join(dataDir(), "config.json")
		b, err := os.ReadFile(old)
		if err != nil {
			return
		}
		var c client.Config
		if json.Unmarshal(b, &c) != nil || c.ServerFP == "" {
			return
		}
		id := serverID(c.ServerFP)
		if os.MkdirAll(serverDir(id), 0o700) != nil {
			return
		}
		if os.Rename(filepath.Join(dataDir(), "key.bin"), keyPath(id)) != nil {
			return
		}
		writeAtomic(drivePath(id), []byte(getSettings().Drive))
		os.Rename(old, configPath(id))
	})
}

// listServers returns the ids of all paired servers, oldest pairing first.
func listServers() []string {
	migrateLegacy()
	ents, _ := os.ReadDir(serversDir())
	type item struct {
		id string
		c  client.Config
	}
	var items []item
	for _, e := range ents {
		if !e.IsDir() || !validID(e.Name()) {
			continue
		}
		if c, err := loadConfig(e.Name()); err == nil {
			items = append(items, item{e.Name(), c})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].c.PairedAt.Before(items[j].c.PairedAt) })
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.id
	}
	return ids
}

func loadConfig(id string) (client.Config, error) {
	var c client.Config
	b, err := os.ReadFile(configPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return c, errNotPaired
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

func saveConfig(id string, c client.Config) error {
	if err := os.MkdirAll(serverDir(id), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return writeAtomic(configPath(id), b)
}

func loadKey(id string) (*ecdsa.PrivateKey, error) {
	blob, err := os.ReadFile(keyPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, errNotPaired
	}
	if err != nil {
		return nil, err
	}
	der, err := dpapi(blob, false)
	if err != nil {
		return nil, err
	}
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	ek, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("key.bin: not an ECDSA key")
	}
	return ek, nil
}

func saveKey(id string, k *ecdsa.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return err
	}
	blob, err := dpapi(der, true)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(serverDir(id), 0o700); err != nil {
		return err
	}
	return writeAtomic(keyPath(id), blob)
}

// serverDrive is the preferred drive letter of a server ("" if none yet).
func serverDrive(id string) string {
	b, _ := os.ReadFile(drivePath(id))
	return normLetter(string(b))
}

func setServerDrive(id, letter string) {
	if l := normLetter(letter); l != "" && validID(id) && os.MkdirAll(serverDir(id), 0o700) == nil {
		writeAtomic(drivePath(id), []byte(l))
	}
}

// normLetter turns "p", "P:" or " P " into "P"; anything else into "".
func normLetter(s string) string {
	s = strings.ToUpper(strings.TrimSuffix(strings.TrimSpace(s), ":"))
	if len(s) != 1 || s[0] < 'D' || s[0] > 'Z' {
		return ""
	}
	return s
}

// newDriveLetter picks a preferred letter for a newly paired server: the
// first free letter from P on that no other server prefers.
func newDriveLetter(except string) string {
	taken := map[string]bool{}
	for _, id := range listServers() {
		if id != except {
			taken[serverDrive(id)] = true
		}
	}
	used, _ := windows.GetLogicalDrives()
	for _, r := range []struct{ from, to byte }{{'P', 'Z'}, {'D', 'O'}} {
		for l := r.from; l <= r.to; l++ {
			if !taken[string(l)] && used&(1<<(l-'A')) == 0 {
				return string(l)
			}
		}
	}
	return "P"
}

// forget removes one pairing (config, private key and drive preference).
func forget(id string) {
	if validID(id) {
		os.RemoveAll(serverDir(id))
	}
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// dpapi encrypts (protect=true) or decrypts data with the current Windows
// user's DPAPI key, so key.bin is useless on another account or computer.
func dpapi(data []byte, protect bool) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("dpapi: empty input")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	desc, _ := windows.UTF16PtrFromString("NASfone device key")
	var err error
	if protect {
		err = windows.CryptProtectData(&in, desc, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
