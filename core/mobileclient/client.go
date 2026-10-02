// Package mobileclient is the gomobile bridge for the NASfone Android client:
// pairing with a key held by the Android Keystore, choosing the fastest way
// to reach a server (LAN, tailnet, Funnel) and file operations over WebDAV.
//
// Paths are server paths like "/Photos/a.jpg". Errors for a device the
// server no longer knows start with "revoked:" so the app can ask to pair
// again instead of retrying.
package mobileclient

import (
	"bytes"
	"context"
	"crypto"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"nasfone/core/auth"
	"nasfone/core/client"
	"nasfone/core/pair"
	"nasfone/core/server"
)

// KeySigner is implemented by the app with an Android Keystore key
// (EC P-256, never leaves the secure hardware).
type KeySigner interface {
	// PublicKeyDER returns the public key as DER SubjectPublicKeyInfo.
	PublicKeyDER() ([]byte, error)
	// SignDigest signs a SHA-256 digest ("NONEwithECDSA") and returns an
	// ASN.1 DER signature.
	SignDigest(digest []byte) ([]byte, error)
}

// Progress receives transfer progress; total is -1 when unknown.
type Progress interface {
	OnProgress(done, total int64)
}

// keystoreSigner adapts KeySigner to crypto.Signer for the shared client code.
type keystoreSigner struct {
	k   KeySigner
	pub crypto.PublicKey
}

func newSigner(k KeySigner) (*keystoreSigner, error) {
	if k == nil {
		return nil, errors.New("no key")
	}
	der, err := k.PublicKeyDER()
	if err != nil {
		return nil, err
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	return &keystoreSigner{k: k, pub: pub}, nil
}

func (s *keystoreSigner) Public() crypto.PublicKey { return s.pub }

func (s *keystoreSigner) Sign(_ io.Reader, digest []byte, _ crypto.SignerOpts) ([]byte, error) {
	return s.k.SignDigest(digest)
}

// metaClient is for short requests (sign-in, listings); transfers use
// streamClient, which has no overall time limit.
var (
	metaClient   = &http.Client{Timeout: 30 * time.Second}
	streamClient = &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 60 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	}}
)

// InviteInfo describes a scanned or pasted invite for the confirmation
// dialog: {"url","host","role","fp"}.
func InviteInfo(text string) (string, error) {
	inv, err := pair.ParseInvite(text)
	if err != nil {
		return "", err
	}
	host := inv.URL
	if u, err := url.Parse(inv.URL); err == nil && u.Host != "" {
		host = u.Host
	}
	b, _ := json.Marshal(map[string]string{
		"url": inv.URL, "host": host, "role": string(inv.Role), "fp": pair.ShortFP(inv.FP),
	})
	return string(b), nil
}

// Pair redeems an invite with the Keystore key and returns the server
// config as JSON (to store; it holds no secret).
func Pair(invite, deviceName string, key KeySigner) (string, error) {
	s, err := newSigner(key)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cfg, err := client.Pair(ctx, metaClient, invite, s, deviceName, "android")
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(cfg)
	return string(b), nil
}

// FingerprintShort formats a server fingerprint like the server shows it.
func FingerprintShort(fp string) string { return pair.ShortFP(fp) }

// Session talks to one paired server. It is safe for concurrent use.
type Session struct {
	cfg    client.Config
	signer *keystoreSigner

	mu       sync.Mutex
	base     string    // URL in use, "" until connected
	token    string    // bearer token
	expires  time.Time // token expiry
	role     auth.Role // role the server reports
	addrs    []string  // known addresses, fastest first
	lastFail time.Time
}

// NewSession opens a session for a stored config. knownAddrsJSON may be ""
// or a JSON array saved from Addrs() earlier.
func NewSession(configJSON, knownAddrsJSON string, key KeySigner) (*Session, error) {
	var cfg client.Config
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, err
	}
	s, err := newSigner(key)
	if err != nil {
		return nil, err
	}
	sess := &Session{cfg: cfg, signer: s, role: cfg.Role}
	if knownAddrsJSON != "" {
		json.Unmarshal([]byte(knownAddrsJSON), &sess.addrs)
	}
	return sess, nil
}

// revokedErr marks errors that mean "pair again".
func wrap(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, client.ErrRevoked) {
		return fmt.Errorf("revoked: %w", err)
	}
	return err
}

// candidates are the addresses to try, fastest first, always ending with
// the address from pairing.
func (s *Session) candidates() []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range append(append([]string(nil), s.addrs...), s.cfg.URL) {
		a = strings.TrimRight(a, "/")
		if a != "" && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}

// probeTimeout: local addresses fail fast; the public one gets more time.
func probeTimeout(base string) time.Duration {
	if u, err := url.Parse(base); err == nil {
		h := u.Hostname()
		if strings.HasPrefix(h, "192.168.") || strings.HasPrefix(h, "10.") || strings.HasPrefix(h, "172.") {
			return 3 * time.Second
		}
		if u.Scheme == "http" {
			return 5 * time.Second // tailnet name: needs the Tailscale app on this phone
		}
	}
	return 20 * time.Second
}

// connect signs in on the first address that answers and proves it is the
// paired server. With force it starts over even if a token is still valid.
func (s *Session) connect(force bool) error {
	s.mu.Lock()
	if !force && s.base != "" && time.Until(s.expires) > 5*time.Minute {
		s.mu.Unlock()
		return nil
	}
	cands := s.candidates()
	if !force && s.base != "" {
		// Token refresh: keep the address that works.
		cands = append([]string{s.base}, cands...)
	}
	s.mu.Unlock()

	var firstErr error
	tried := map[string]bool{}
	for _, base := range cands {
		if tried[base] {
			continue
		}
		tried[base] = true
		cfg := s.cfg
		cfg.URL = base
		ctx, cancel := context.WithTimeout(context.Background(), probeTimeout(base))
		tok, err := client.Authenticate(ctx, metaClient, cfg, s.signer)
		cancel()
		if err != nil {
			if errors.Is(err, client.ErrRevoked) {
				return wrap(err) // the server answered: no point trying other paths
			}
			if firstErr == nil || base == strings.TrimRight(s.cfg.URL, "/") {
				firstErr = err
			}
			continue
		}
		s.mu.Lock()
		s.base, s.token, s.expires, s.role = base, tok.Value, tok.Expires, tok.Role
		s.mu.Unlock()
		s.refreshAddrs()
		return nil
	}
	if firstErr == nil {
		firstErr = errors.New("no address to reach the server")
	}
	s.mu.Lock()
	s.lastFail = time.Now()
	s.mu.Unlock()
	return firstErr
}

// refreshAddrs asks the server where else it can be reached.
func (s *Session) refreshAddrs() {
	var r struct {
		URLs []string `json:"urls"`
	}
	res, err := s.do(context.Background(), "GET", server.AddrsPath, nil, nil, false)
	if err != nil {
		return
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&r) != nil {
		return
	}
	s.mu.Lock()
	s.addrs = r.URLs
	s.mu.Unlock()
}

// Connect signs in (choosing the best address) and returns
// {"base","via","role"} where via is "lan", "tailnet" or "internet".
func (s *Session) Connect() (string, error) {
	if err := s.connect(false); err != nil {
		return "", wrap(err)
	}
	return s.Info(), nil
}

// Reconnect forgets the current address and picks the best one again
// (call it when the phone's network changes).
func (s *Session) Reconnect() (string, error) {
	if err := s.connect(true); err != nil {
		return "", wrap(err)
	}
	return s.Info(), nil
}

// Info returns {"base","via","role"} for the current connection.
func (s *Session) Info() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.Marshal(map[string]string{"base": s.base, "via": viaOf(s.base), "role": string(s.role)})
	return string(b)
}

func viaOf(base string) string {
	u, err := url.Parse(base)
	if err != nil || base == "" {
		return ""
	}
	switch {
	case u.Scheme == "https":
		return "internet"
	case strings.Count(u.Hostname(), ".") == 3 && !strings.HasPrefix(u.Hostname(), "100."):
		return "lan"
	default:
		return "tailnet"
	}
}

// Addrs returns the known server addresses as JSON (for the app to store).
func (s *Session) Addrs() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.Marshal(s.addrs)
	if s.addrs == nil {
		return "[]"
	}
	return string(b)
}

// CanWrite reports whether this device has the admin role.
func (s *Session) CanWrite() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.role == auth.RoleAdmin
}

// escapePath percent-encodes each segment of a server path.
func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		parts[i] = url.PathEscape(seg)
	}
	out := strings.Join(parts, "/")
	if !strings.HasPrefix(out, "/") {
		out = "/" + out
	}
	return out
}

// do sends a short authenticated request. On a network error or a refused
// token it signs in again (possibly on another address) and retries once.
func (s *Session) do(ctx context.Context, method, path string, body []byte, hdr map[string]string, retry bool) (*http.Response, error) {
	if err := s.connect(false); err != nil {
		return nil, wrap(err)
	}
	res, err := s.send(metaClient, ctx, method, path, bodyReader(body), hdr)
	if retry && (err != nil || res.StatusCode == http.StatusUnauthorized) {
		if res != nil {
			res.Body.Close()
		}
		if cerr := s.connect(true); cerr != nil {
			return nil, wrap(cerr)
		}
		return s.send(metaClient, ctx, method, path, bodyReader(body), hdr)
	}
	return res, err
}

func bodyReader(b []byte) io.Reader {
	if b == nil {
		return nil
	}
	return bytes.NewReader(b)
}

// send performs one request on the current address with the current token.
func (s *Session) send(hc *http.Client, ctx context.Context, method, path string, body io.Reader, hdr map[string]string) (*http.Response, error) {
	s.mu.Lock()
	base, tok := s.base, s.token
	s.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, method, base+escapePath(path), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return hc.Do(req)
}

func statusErr(res *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
	msg := strings.TrimSpace(string(b))
	if msg == "" {
		msg = res.Status
	}
	return &client.HTTPError{Status: res.StatusCode, Msg: msg}
}
