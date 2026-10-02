//go:build windows

package main

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"

	"nasfone/core/client"
)

// dataDir is %APPDATA%\NASfone: config.json (not secret) and key.bin
// (the device private key, encrypted with DPAPI for this Windows user only).
func dataDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	d := filepath.Join(base, "NASfone")
	os.MkdirAll(d, 0o700)
	return d
}

func configPath() string { return filepath.Join(dataDir(), "config.json") }
func keyPath() string    { return filepath.Join(dataDir(), "key.bin") }

var errNotPaired = errors.New("not paired")

func loadConfig() (client.Config, error) {
	var c client.Config
	b, err := os.ReadFile(configPath())
	if errors.Is(err, os.ErrNotExist) {
		return c, errNotPaired
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

func saveConfig(c client.Config) error {
	b, _ := json.MarshalIndent(c, "", "  ")
	return writeAtomic(configPath(), b)
}

func loadKey() (*ecdsa.PrivateKey, error) {
	blob, err := os.ReadFile(keyPath())
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

func saveKey(k *ecdsa.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return err
	}
	blob, err := dpapi(der, true)
	if err != nil {
		return err
	}
	return writeAtomic(keyPath(), blob)
}

// forget removes the pairing (config and private key).
func forget() {
	os.Remove(configPath())
	os.Remove(keyPath())
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
