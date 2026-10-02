package mobileclient

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nasfone/core/auth"
	"nasfone/core/pair"
	"nasfone/core/server"
)

// testKey plays the Android Keystore.
type testKey struct{ k *ecdsa.PrivateKey }

func newTestKey(t *testing.T) *testKey {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &testKey{k}
}
func (k *testKey) PublicKeyDER() ([]byte, error) { return x509.MarshalPKIXPublicKey(&k.k.PublicKey) }
func (k *testKey) SignDigest(d []byte) ([]byte, error) {
	return ecdsa.SignASN1(rand.Reader, k.k, d)
}

type env struct {
	root          string
	pairs         *pair.Store
	lan, internet *httptest.Server
}

// newEnv runs one server reachable two ways: "internet" (the address in
// invites) and "lan", plus a dead address that must be skipped.
func newEnv(t *testing.T) *env {
	e := &env{root: t.TempDir()}
	os.WriteFile(filepath.Join(e.root, "a.txt"), []byte("hello"), 0o644)
	os.Mkdir(filepath.Join(e.root, "Photos"), 0o755)
	store, _ := auth.Open(filepath.Join(t.TempDir(), "a.json"))
	var err error
	e.pairs, err = pair.Open(filepath.Join(t.TempDir(), "pair"))
	if err != nil {
		t.Fatal(err)
	}
	var addrs []string
	h := server.NewHandler(server.Options{Root: e.root, Auth: store, Pair: e.pairs, Logf: func(string, ...any) {},
		Addresses: func() []string { return addrs }})
	e.lan = httptest.NewServer(server.WithVia(h, server.ViaLAN))
	e.internet = httptest.NewServer(server.WithVia(h, "Funnel"))
	t.Cleanup(e.lan.Close)
	t.Cleanup(e.internet.Close)
	addrs = []string{"http://127.0.0.1:1", e.lan.URL, e.internet.URL}
	return e
}

func (e *env) pair(t *testing.T, role auth.Role) (*Session, *testKey) {
	inv, _ := e.pairs.NewInvite(role, e.internet.URL)
	info, err := InviteInfo(inv)
	if err != nil || !strings.Contains(info, string(role)) {
		t.Fatalf("InviteInfo: %s %v", info, err)
	}
	key := newTestKey(t)
	cfg, err := Pair(inv, "Test phone", key)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(cfg, "", key)
	if err != nil {
		t.Fatal(err)
	}
	return s, key
}

func tempFile(t *testing.T, content string) (*os.File, int64) {
	p := filepath.Join(t.TempDir(), "src")
	os.WriteFile(p, []byte(content), 0o644)
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	return f, int64(len(content))
}

func upload(t *testing.T, s *Session, p, content, mode string) (string, error) {
	f, n := tempFile(t, content)
	return s.Upload(p, int(f.Fd()), n, mode, nil)
}

func TestClientFlow(t *testing.T) {
	e := newEnv(t)
	s, _ := e.pair(t, auth.RoleAdmin)

	info, err := s.Connect()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(info, e.internet.URL) {
		t.Fatalf("first connect should use the invite address: %s", info)
	}
	if !strings.Contains(s.Addrs(), e.lan.URL) {
		t.Fatalf("addresses not learned: %s", s.Addrs())
	}
	// With the addresses known, the dead one is skipped and the LAN one wins.
	info, err = s.Reconnect()
	if err != nil || !strings.Contains(info, e.lan.URL) || !strings.Contains(info, `"via":"lan"`) {
		t.Fatalf("reconnect: %s %v", info, err)
	}

	var list []Entry
	js, err := s.List("/")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(js), &list)
	if len(list) != 2 || !list[0].Dir || list[0].Name != "Photos" || list[1].Name != "a.txt" || list[1].Size != 5 {
		t.Fatalf("list: %s", js)
	}

	// Upload modes.
	if p, err := upload(t, s, "/a.txt", "new", ModeSkip); err != nil || p != "" {
		t.Fatalf("skip: %q %v", p, err)
	}
	if _, err := upload(t, s, "/a.txt", "new", ModeFail); err == nil || !strings.HasPrefix(err.Error(), "exists:") {
		t.Fatalf("fail mode: %v", err)
	}
	if p, err := upload(t, s, "/a.txt", "both", ModeKeepBoth); err != nil || p != "/a (1).txt" {
		t.Fatalf("keep both: %q %v", p, err)
	}
	if p, err := upload(t, s, "/a.txt", "over", ModeOverwrite); err != nil || p != "/a.txt" {
		t.Fatalf("overwrite: %q %v", p, err)
	}
	if b, _ := os.ReadFile(filepath.Join(e.root, "a.txt")); string(b) != "over" {
		t.Fatalf("content after overwrite: %q", b)
	}
	if p, err := upload(t, s, "/Photos/Tết 2026 #1.jpg", "img", ModeFail); err != nil || p != "/Photos/Tết 2026 #1.jpg" {
		t.Fatalf("unicode name: %q %v", p, err)
	}

	// Download into a file descriptor.
	out := filepath.Join(t.TempDir(), "dl")
	f, _ := os.Create(out)
	if err := s.Download("/Photos/Tết 2026 #1.jpg", int(f.Fd()), nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != "img" {
		t.Fatalf("download: %q", b)
	}

	// Folders, move, delete, stat, quota.
	if err := s.Mkdir("/New folder"); err != nil {
		t.Fatal(err)
	}
	if err := s.Move("/a (1).txt", "/New folder/b.txt"); err != nil {
		t.Fatal(err)
	}
	if js, err := s.Stat("/New folder/b.txt"); err != nil || !strings.Contains(js, `"size":4`) {
		t.Fatalf("stat: %s %v", js, err)
	}
	if err := s.Delete("/New folder"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stat("/New folder"); err == nil || !strings.HasPrefix(err.Error(), "notfound:") {
		t.Fatalf("stat deleted: %v", err)
	}
	if js, err := s.Quota(); err != nil || !strings.Contains(js, "avail") || strings.Contains(js, `"avail":-1`) {
		t.Fatalf("quota: %s %v", js, err)
	}

	// Revoked on the server: the error says so (the app then asks to pair again).
	for _, d := range e.pairs.List() {
		e.pairs.Revoke(d.ID)
	}
	if _, err := s.Reconnect(); err == nil || !strings.HasPrefix(err.Error(), "revoked:") {
		t.Fatalf("revoked: %v", err)
	}
}

func TestClientUserIsReadOnly(t *testing.T) {
	e := newEnv(t)
	s, _ := e.pair(t, auth.RoleUser)
	if _, err := s.Connect(); err != nil {
		t.Fatal(err)
	}
	if s.CanWrite() {
		t.Fatal("user role can write")
	}
	if _, err := upload(t, s, "/x.txt", "x", ModeOverwrite); err == nil || !strings.HasPrefix(err.Error(), "readonly:") {
		t.Fatalf("user upload: %v", err)
	}
	if err := s.Delete("/a.txt"); err == nil {
		t.Fatal("user deleted a file")
	}
}

func TestQRText(t *testing.T) {
	if QRText("nasfone1:abc") != "invite" || QRText("NASfone://pair?i=x") != "invite" || QRText("https://example.com") != "" {
		t.Fatal("QRText")
	}
}
