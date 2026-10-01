package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nasfone/core/auth"
	"nasfone/core/pair"
	"nasfone/core/server"
)

type env struct {
	root  string
	pairs *pair.Store
	srv   *httptest.Server
	ctx   context.Context
	hc    *http.Client
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644)
	sessions, _ := auth.Open(filepath.Join(t.TempDir(), "s.json"))
	ps, err := pair.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.WithVia(server.NewHandler(server.Options{Root: root, Auth: sessions, Pair: ps}), "Tailnet"))
	t.Cleanup(srv.Close)
	return &env{root: root, pairs: ps, srv: srv, ctx: context.Background(), hc: srv.Client()}
}

func (e *env) get(t *testing.T, token, method, path, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := e.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestPairAuthAndUse(t *testing.T) {
	e := newEnv(t)
	inv, _ := e.pairs.NewInvite(auth.RoleAdmin, e.srv.URL)
	key, _ := GenerateKey()
	cfg, err := Pair(e.ctx, e.hc, "  "+inv+"\n", key, "Laptop", "windows")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Role != auth.RoleAdmin || cfg.ServerFP != e.pairs.Fingerprint() || cfg.URL != e.srv.URL {
		t.Fatalf("cfg %+v", cfg)
	}
	// The invite is single use.
	if _, err := Pair(e.ctx, e.hc, inv, key, "Again", "windows"); err == nil {
		t.Fatal("invite reused")
	}

	tok, err := Authenticate(e.ctx, e.hc, cfg, key)
	if err != nil || tok.Value == "" || tok.Role != auth.RoleAdmin {
		t.Fatalf("auth: %v %+v", err, tok)
	}
	if r := e.get(t, tok.Value, "GET", "/a.txt", ""); r.StatusCode != 200 {
		t.Fatalf("bearer get: %d", r.StatusCode)
	}
	if r := e.get(t, tok.Value, "PUT", "/b.txt", "x"); r.StatusCode != 201 {
		t.Fatalf("admin bearer put: %d", r.StatusCode)
	}
	if r := e.get(t, "bogus", "GET", "/a.txt", ""); r.StatusCode != 401 {
		t.Fatalf("bogus bearer: %d", r.StatusCode)
	}

	// An admin web session can mint an invite link for this computer.
	if r := e.get(t, tok.Value, "POST", "/__nasfone/invite", ""); r.StatusCode != 400 { // wrong content type
		t.Fatalf("invite content-type check: %d", r.StatusCode)
	}

	// Revoking the device kills its token immediately and blocks new sign-ins.
	if !e.pairs.Revoke(cfg.DeviceID) {
		t.Fatal("revoke")
	}
	if r := e.get(t, tok.Value, "GET", "/a.txt", ""); r.StatusCode != 401 {
		t.Fatalf("revoked token still works: %d", r.StatusCode)
	}
	if _, err := Authenticate(e.ctx, e.hc, cfg, key); err == nil {
		t.Fatal("revoked device signed in")
	}
}

func TestUserRoleAppIsReadOnly(t *testing.T) {
	e := newEnv(t)
	inv, _ := e.pairs.NewInvite(auth.RoleUser, e.srv.URL)
	key, _ := GenerateKey()
	cfg, err := Pair(e.ctx, e.hc, inv, key, "Phone", "android")
	if err != nil || cfg.Role != auth.RoleUser {
		t.Fatalf("pair: %v %+v", err, cfg)
	}
	tok, _ := Authenticate(e.ctx, e.hc, cfg, key)
	if r := e.get(t, tok.Value, "GET", "/a.txt", ""); r.StatusCode != 200 {
		t.Fatalf("user read: %d", r.StatusCode)
	}
	for _, m := range []string{"PUT", "DELETE", "MKCOL"} {
		if r := e.get(t, tok.Value, m, "/a.txt", "x"); r.StatusCode != 403 {
			t.Errorf("user %s: %d", m, r.StatusCode)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(e.root, "a.txt")); string(b) != "hello" {
		t.Fatal("user app changed a file")
	}
	// Admin can later promote the device; it applies on the next request.
	e.pairs.SetRole(cfg.DeviceID, auth.RoleAdmin)
	if r := e.get(t, tok.Value, "PUT", "/a.txt", "promoted"); r.StatusCode != 204 {
		t.Fatalf("after promotion: %d", r.StatusCode)
	}
}

func TestFakeServerIsRejected(t *testing.T) {
	real := newEnv(t)
	fake := newEnv(t) // a different server with a different identity key
	// The invite promises the real server's key but points at the fake one.
	inv, _ := real.pairs.NewInvite(auth.RoleAdmin, real.srv.URL)
	decoded, _ := pair.ParseInvite(inv)
	fakeInvite, _ := fake.pairs.NewInvite(auth.RoleAdmin, fake.srv.URL)
	fd, _ := pair.ParseInvite(fakeInvite)
	decoded.URL, decoded.Token = fake.srv.URL, fd.Token // attacker redirects the client
	b, _ := json.Marshal(decoded)
	tampered := "nasfone1:" + b64url(b)

	key, _ := GenerateKey()
	if _, err := Pair(real.ctx, real.hc, tampered, key, "Victim", "windows"); err == nil || !strings.Contains(err.Error(), "giả mạo") {
		t.Fatalf("fake server accepted: %v", err)
	}

	// After pairing with the real server, a fake server cannot pass sign-in.
	inv, _ = real.pairs.NewInvite(auth.RoleAdmin, real.srv.URL)
	cfg, err := Pair(real.ctx, real.hc, inv, key, "Victim", "windows")
	if err != nil {
		t.Fatal(err)
	}
	cfg.URL = fake.srv.URL
	if _, err := Authenticate(real.ctx, fake.hc, cfg, key); err == nil {
		t.Fatal("signed in to a server without the pinned key")
	}
}

func TestStolenDeviceIDWithoutKeyFails(t *testing.T) {
	e := newEnv(t)
	inv, _ := e.pairs.NewInvite(auth.RoleAdmin, e.srv.URL)
	key, _ := GenerateKey()
	cfg, _ := Pair(e.ctx, e.hc, inv, key, "Laptop", "windows")
	other, _ := GenerateKey() // attacker knows deviceId and server key but not the private key
	if _, err := Authenticate(e.ctx, e.hc, cfg, other); err == nil {
		t.Fatal("authenticated without the device's private key")
	}
}

func TestInviteLinkFromWebSession(t *testing.T) {
	e := newEnv(t)
	// Sign in as admin through the web (6-digit code) and ask for an app link.
	sessions, _ := auth.Open(filepath.Join(t.TempDir(), "s.json"))
	srv := httptest.NewServer(server.WithVia(server.NewHandler(server.Options{Root: e.root, Auth: sessions, Pair: e.pairs}), "Funnel"))
	defer srv.Close()
	adminCode, userCode, _ := sessions.CurrentCodes()
	adminTok, _, _ := sessions.Redeem(adminCode, "PC", "1.1.1.1", "Funnel")
	userTok, _, _ := sessions.Redeem(userCode, "Guest", "1.1.1.1", "Funnel")

	ask := func(cookie, role string) *http.Response {
		req, _ := http.NewRequest("POST", srv.URL+"/__nasfone/invite", strings.NewReader(`{"role":"`+role+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "nasfone_s", Value: cookie})
		res, _ := http.DefaultClient.Do(req)
		return res
	}
	if r := ask(userTok, "user"); r.StatusCode != 403 {
		t.Fatalf("user session created an invite: %d", r.StatusCode)
	}
	r := ask(adminTok, "user")
	var out struct{ Invite, Link string }
	b, _ := io.ReadAll(r.Body)
	json.Unmarshal(b, &out)
	if r.StatusCode != 200 || !strings.HasPrefix(out.Link, "nasfone://pair?i=nasfone1%3A") {
		t.Fatalf("invite: %d %s", r.StatusCode, b)
	}
	// The app is launched with the escaped nasfone:// link itself.
	inv, err := pair.ParseInvite(out.Link)
	if err != nil || inv.URL != srv.URL || inv.Role != auth.RoleUser {
		t.Fatalf("parse link: %v %+v", err, inv)
	}
	key, _ := GenerateKey()
	cfg, err := Pair(e.ctx, srv.Client(), out.Link, key, "This PC", "windows")
	if err != nil || cfg.Role != auth.RoleUser {
		t.Fatalf("pair via web invite: %v %+v", err, cfg)
	}
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
