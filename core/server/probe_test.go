package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nasfone/core/auth"
	"nasfone/core/pair"
)

func probeServer(t *testing.T) (root string, store *auth.Store, pairs *pair.Store, srv *httptest.Server) {
	t.Helper()
	root = t.TempDir()
	state := filepath.Join(root, "AppData", "Roaming", "NASfone-Server")
	os.MkdirAll(state, 0o700)
	os.WriteFile(filepath.Join(state, "auth.json"), []byte("SECRET"), 0o600)
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644)
	store, _ = auth.Open(filepath.Join(t.TempDir(), "a.json"))
	pairs, _ = pair.Open(filepath.Join(t.TempDir(), "pair"))
	srv = httptest.NewServer(WithVia(NewHandler(Options{
		Root: root, Auth: store, Pair: pairs,
		Hidden: func() []string { return []string{state} },
	}), "Funnel"))
	t.Cleanup(srv.Close)
	return
}

func send(t *testing.T, method, u string, hdr map[string]string, cookie, bearer string) (int, []*http.Cookie) {
	t.Helper()
	req, _ := http.NewRequest(method, u, strings.NewReader("x"))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "nasfone_s", Value: cookie})
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res.StatusCode, res.Cookies()
}

// Five requests for a hidden folder lock a browser session, admin or user;
// other sessions are not affected.
func TestProbingLocksBrowserSession(t *testing.T) {
	for _, role := range []auth.Role{auth.RoleAdmin, auth.RoleUser} {
		t.Run(string(role), func(t *testing.T) {
			_, store, _, srv := probeServer(t)
			adminCode, userCode, _ := store.CurrentCodes()
			code := userCode
			if role == auth.RoleAdmin {
				code = adminCode
			}
			tok, dev, err := store.Redeem(code, "Prober", "1.1.1.1", "Funnel")
			if err != nil || dev.Role != role {
				t.Fatal(err, dev.Role)
			}
			// A bystander on the other role keeps working.
			other := adminCode
			if role == auth.RoleAdmin {
				_, other, _ = store.CurrentCodes()
			}
			bystander, _, err := store.Redeem(other, "Bystander", "2.2.2.2", "Funnel")
			if err != nil {
				t.Fatal(err)
			}

			// Mixed methods and spellings all count, including a COPY that only
			// aims its Destination at the hidden folder.
			tries := []struct {
				method, path string
				hdr          map[string]string
			}{
				{"GET", "/AppData/Roaming/NASfone-Server/auth.json", nil},
				{"PROPFIND", "/AppData/Roaming/NASfone-Server/", map[string]string{"Depth": "1"}},
				{"PUT", "/AppData/Roaming/NASfone-Server/x.txt", nil},
				{"COPY", "/a.txt", map[string]string{"Destination": srv.URL + "/AppData/Roaming/NASfone-Server/y.txt"}},
			}
			for i, tr := range tries {
				if c, _ := send(t, tr.method, srv.URL+tr.path, tr.hdr, tok, ""); c != 404 {
					t.Fatalf("try %d (%s): %d, want 404", i+1, tr.method, c)
				}
			}
			if c, _ := send(t, "GET", srv.URL+"/a.txt", nil, tok, ""); c != 200 {
				t.Fatalf("after 4 tries the session should still work: %d", c)
			}
			c, cookies := send(t, "GET", srv.URL+"/AppData/Roaming/NASfone-Server/auth.json", nil, tok, "")
			if c != 403 {
				t.Fatalf("5th try: %d, want 403", c)
			}
			cleared := false
			for _, ck := range cookies {
				if ck.Name == "nasfone_s" && ck.MaxAge < 0 {
					cleared = true
				}
			}
			if !cleared {
				t.Error("cookie not cleared")
			}
			if c, _ := send(t, "GET", srv.URL+"/a.txt", nil, tok, ""); c != 401 {
				t.Fatalf("locked session still works: %d", c)
			}
			for _, d := range store.List() {
				if d.ID == dev.ID {
					t.Fatal("locked session still listed")
				}
			}
			if c, _ := send(t, "GET", srv.URL+"/a.txt", nil, bystander, ""); c != 200 {
				t.Fatalf("bystander locked out: %d", c)
			}
		})
	}
}

// A paired app (even an admin one) that probes is unpaired.
func TestProbingUnpairsApp(t *testing.T) {
	_, _, pairs, srv := probeServer(t)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	pub := base64.StdEncoding.EncodeToString(der)
	inv, _ := pairs.NewInvite(auth.RoleAdmin, srv.URL)
	parsed, _ := pair.ParseInvite(inv)
	resp, err := pairs.Pair(pair.PairRequest{Token: parsed.Token, PubKey: pub, Name: "App", Platform: "test", NonceC: "nonce-c-0123456789"}, "1.1.1.1", "Funnel")
	if err != nil {
		t.Fatal(err)
	}
	nonceC := "nonce-c-abcdefghij"
	nonceS, _, err := pairs.Challenge(resp.DeviceID, nonceC, "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256Sum(pair.AuthMessage(resp.DeviceID, nonceS, nonceC, pairs.Fingerprint()))
	sig, _ := ecdsa.SignASN1(rand.Reader, key, sum)
	tok, _, _, err := pairs.Auth(resp.DeviceID, nonceS, base64.StdEncoding.EncodeToString(sig), "1.1.1.1", "Funnel")
	if err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= probeLimit; i++ {
		want := 404
		if i == probeLimit {
			want = 403
		}
		if c, _ := send(t, "GET", srv.URL+"/AppData/Roaming/NASfone-Server/auth.json", nil, "", tok); c != want {
			t.Fatalf("try %d: %d, want %d", i, c, want)
		}
	}
	if c, _ := send(t, "GET", srv.URL+"/a.txt", nil, "", tok); c != 401 {
		t.Fatalf("unpaired app still works: %d", c)
	}
	if len(pairs.List()) != 0 {
		t.Fatal("app still paired")
	}
}

// Requests without a session are not counted (they are refused anyway), and
// an unrelated missing file does not count as probing.
func TestMissingFilesDoNotCount(t *testing.T) {
	_, store, _, srv := probeServer(t)
	adminCode, _, _ := store.CurrentCodes()
	tok, _, _ := store.Redeem(adminCode, "Owner", "1.1.1.1", "Funnel")
	for i := 0; i < 2*probeLimit; i++ {
		if c, _ := send(t, "GET", srv.URL+"/nope.txt", nil, tok, ""); c != 404 {
			t.Fatalf("missing file: %d", c)
		}
		if c, _ := send(t, "GET", srv.URL+"/AppData/Roaming/NASfone-Server/auth.json", nil, "", ""); c != 401 {
			t.Fatalf("anonymous: %d", c)
		}
	}
	if c, _ := send(t, "GET", srv.URL+"/a.txt", nil, tok, ""); c != 200 {
		t.Fatalf("session locked by unrelated 404s: %d", c)
	}
}

func sha256Sum(b []byte) []byte { s := sha256.Sum256(b); return s[:] }
