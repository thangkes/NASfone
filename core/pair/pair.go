// Package pair implements key-pair pairing between the PocketNAS server and
// its client apps (see docs/PAIRING.md):
//
//   - the server has a long-lived ECDSA P-256 identity key;
//   - an owner (or a signed-in web session) creates a one-time invite that
//     carries the server URL, the server key fingerprint and a token;
//   - a client generates its own key pair, redeems the invite with its
//     public key, and pins the server key;
//   - afterwards both sides prove possession of their keys (challenge /
//     response) and the client gets a short-lived bearer token.
package pair

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"pocketnas/core/auth"
)

const (
	InviteTTL    = 5 * time.Minute
	TokenTTL     = time.Hour
	challengeTTL = time.Minute
	failWindow   = 15 * time.Minute
	maxFailures  = 20 // per IP across pair/auth attempts

	invitePrefix = "pnas1:"

	ctxPair  = "pnas-pair-v1"
	ctxAuthS = "pnas-auth-s-v1"
	ctxAuthC = "pnas-auth-c-v1"
)

var (
	ErrBadInvite  = errors.New("lời mời không hợp lệ, đã dùng hoặc đã hết hạn")
	ErrBadKey     = errors.New("khóa công khai không hợp lệ (cần ECDSA P-256)")
	ErrUnknown    = errors.New("thiết bị chưa ghép đôi hoặc đã bị thu hồi")
	ErrBadSig     = errors.New("chữ ký không hợp lệ")
	ErrRateLimit  = errors.New("thử sai quá nhiều lần, vui lòng đợi")
	ErrBadRequest = errors.New("yêu cầu không hợp lệ")
)

// Device is a paired client app.
type Device struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Platform string    `json:"platform"`
	Role     auth.Role `json:"role"`
	PubKey   string    `json:"pubKey"` // base64 (std) DER SubjectPublicKeyInfo
	FP       string    `json:"fp"`     // hex SHA-256 of the DER public key
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"lastSeen"`
	LastIP   string    `json:"lastIP"`
	Via      string    `json:"via"`
}

// Invite is the decoded content of an invite string.
type Invite struct {
	V     int       `json:"v"`
	URL   string    `json:"u"` // base URL the client should use, e.g. https://pocketnas.x.ts.net
	FP    string    `json:"f"` // server key fingerprint (hex SHA-256 of DER SPKI)
	Token string    `json:"t"`
	Role  auth.Role `json:"r"`
}

type invite struct {
	role auth.Role
	exp  time.Time
}

type access struct {
	deviceID string
	exp      time.Time
}

type challenge struct {
	deviceID string
	nonceC   string
	exp      time.Time
}

// Store holds the server identity, paired devices and the in-memory
// invites, challenges and access tokens.
type Store struct {
	mu         sync.Mutex
	dir        string
	key        *ecdsa.PrivateKey
	pubDER     []byte
	fp         string
	devices    []*Device
	invites    map[string]*invite    // token hash -> invite
	challenges map[string]*challenge // nonce_s -> challenge
	tokens     map[string]*access    // token hash -> access
	fails      map[string][]time.Time

	Now     func() time.Time
	OnEvent func(kind, detail string) // "paired", "unpaired"
}

// Open loads the server identity (creating it on first run) and the paired
// device list from dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{
		dir:        dir,
		invites:    map[string]*invite{},
		challenges: map[string]*challenge{},
		tokens:     map[string]*access{},
		fails:      map[string][]time.Time{},
		Now:        time.Now,
	}
	key, err := loadOrCreateKey(filepath.Join(dir, "identity.pem"))
	if err != nil {
		return nil, err
	}
	s.key = key
	s.pubDER, _ = x509.MarshalPKIXPublicKey(&key.PublicKey)
	sum := sha256.Sum256(s.pubDER)
	s.fp = hex.EncodeToString(sum[:])

	b, err := os.ReadFile(filepath.Join(dir, "paired.json"))
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &s.devices); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func loadOrCreateKey(path string) (*ecdsa.PrivateKey, error) {
	if b, err := os.ReadFile(path); err == nil {
		blk, _ := pem.Decode(b)
		if blk == nil {
			return nil, errors.New("identity.pem: bad PEM")
		}
		return x509.ParseECPrivateKey(blk.Bytes)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, err
	}
	return key, os.Rename(tmp, path)
}

// Fingerprint is the hex SHA-256 of the server's public key.
func (s *Store) Fingerprint() string { return s.fp }

// ShortFP renders a fingerprint for people to compare: "ABCD-EF01-2345-6789".
func ShortFP(fp string) string {
	fp = strings.ToUpper(fp)
	if len(fp) < 16 {
		return fp
	}
	return fp[0:4] + "-" + fp[4:8] + "-" + fp[8:12] + "-" + fp[12:16]
}

// NewInvite creates a one-time invite for baseURL with the given role and
// returns it encoded as "pnas1:<base64url JSON>".
func (s *Store) NewInvite(role auth.Role, baseURL string) (string, time.Time) {
	if role != auth.RoleAdmin {
		role = auth.RoleUser
	}
	token := randToken()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	s.pruneLocked(now)
	exp := now.Add(InviteTTL)
	s.invites[hashHex(token)] = &invite{role: role, exp: exp}
	b, _ := json.Marshal(Invite{V: 1, URL: strings.TrimRight(baseURL, "/"), FP: s.fp, Token: token, Role: role})
	return invitePrefix + base64.RawURLEncoding.EncodeToString(b), exp
}

// ParseInvite decodes an invite string (also accepted with surrounding
// whitespace or as the i= value of a pocketnas://pair link).
func ParseInvite(sIn string) (Invite, error) {
	sIn = strings.TrimSpace(sIn)
	if strings.HasPrefix(strings.ToLower(sIn), "pocketnas:") {
		// pocketnas://pair?i=<url-escaped invite>, as handed over by the browser
		if u, err := url.Parse(sIn); err == nil {
			sIn = u.Query().Get("i")
		}
	}
	if i := strings.Index(sIn, invitePrefix); i >= 0 {
		sIn = sIn[i+len(invitePrefix):]
	} else {
		return Invite{}, ErrBadInvite
	}
	if j := strings.IndexAny(sIn, "&# \r\n"); j >= 0 {
		sIn = sIn[:j]
	}
	b, err := base64.RawURLEncoding.DecodeString(sIn)
	if err != nil {
		return Invite{}, ErrBadInvite
	}
	var inv Invite
	if err := json.Unmarshal(b, &inv); err != nil || inv.V != 1 || inv.URL == "" || len(inv.FP) != 64 || inv.Token == "" {
		return Invite{}, ErrBadInvite
	}
	return inv, nil
}

// PairRequest is sent by a client to redeem an invite.
type PairRequest struct {
	Token    string `json:"token"`
	PubKey   string `json:"pubKey"` // base64 DER SPKI, ECDSA P-256
	Name     string `json:"name"`
	Platform string `json:"platform"`
	NonceC   string `json:"nonceC"`
}

// PairResponse proves the server's identity to the client.
type PairResponse struct {
	DeviceID  string    `json:"deviceId"`
	Role      auth.Role `json:"role"`
	ServerKey string    `json:"serverKey"` // base64 DER SPKI
	Sig       string    `json:"sig"`       // base64 ASN.1 ECDSA over PairMessage(...)
}

// PairMessage is the transcript the server signs when pairing.
func PairMessage(token, clientPub, deviceID, nonceC string) []byte {
	return []byte(strings.Join([]string{ctxPair, token, clientPub, deviceID, nonceC}, "\n"))
}

// Pair redeems an invite and registers the client's public key.
func (s *Store) Pair(req PairRequest, ip, via string) (PairResponse, error) {
	if len(req.NonceC) < 16 || len(req.NonceC) > 128 {
		return PairResponse{}, ErrBadRequest
	}
	pub, err := parsePub(req.PubKey)
	if err != nil {
		return PairResponse{}, ErrBadKey
	}
	var event, detail string
	defer func() {
		if event != "" && s.OnEvent != nil {
			s.OnEvent(event, detail)
		}
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	if s.limitedLocked(ip, now) {
		return PairResponse{}, ErrRateLimit
	}
	s.pruneLocked(now)
	h := hashHex(req.Token)
	inv, ok := s.invites[h]
	if !ok {
		s.failLocked(ip, now)
		return PairResponse{}, ErrBadInvite
	}
	delete(s.invites, h) // single use, even if anything below fails

	der, _ := x509.MarshalPKIXPublicKey(pub)
	sum := sha256.Sum256(der)
	name := clean(req.Name, "Thiết bị")
	d := &Device{
		ID:       randID(),
		Name:     name,
		Platform: clean(req.Platform, "?"),
		Role:     inv.role,
		PubKey:   base64.StdEncoding.EncodeToString(der),
		FP:       hex.EncodeToString(sum[:]),
		Created:  now,
		LastSeen: now,
		LastIP:   ip,
		Via:      via,
	}
	s.devices = append(s.devices, d)
	if err := s.saveLocked(); err != nil {
		return PairResponse{}, err
	}
	sig, err := s.sign(PairMessage(req.Token, req.PubKey, d.ID, req.NonceC))
	if err != nil {
		return PairResponse{}, err
	}
	event, detail = "paired", name+" ("+string(d.Role)+")"
	return PairResponse{DeviceID: d.ID, Role: d.Role, ServerKey: base64.StdEncoding.EncodeToString(s.pubDER), Sig: sig}, nil
}

// ChallengeMessage is what the server signs to prove itself at sign-in.
func ChallengeMessage(deviceID, nonceC, nonceS string) []byte {
	return []byte(strings.Join([]string{ctxAuthS, deviceID, nonceC, nonceS}, "\n"))
}

// AuthMessage is what the client signs to prove itself at sign-in. It is
// bound to the server's key fingerprint so it cannot be replayed elsewhere.
func AuthMessage(deviceID, nonceS, nonceC, serverFP string) []byte {
	return []byte(strings.Join([]string{ctxAuthC, deviceID, nonceS, nonceC, serverFP}, "\n"))
}

// Challenge starts a sign-in: the server answers with its own nonce and a
// signature proving it holds the pinned key.
func (s *Store) Challenge(deviceID, nonceC, ip string) (nonceS, sig string, err error) {
	if len(nonceC) < 16 || len(nonceC) > 128 {
		return "", "", ErrBadRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	if s.limitedLocked(ip, now) {
		return "", "", ErrRateLimit
	}
	s.pruneLocked(now)
	if s.deviceLocked(deviceID) == nil {
		s.failLocked(ip, now)
		return "", "", ErrUnknown
	}
	nonceS = randToken()
	s.challenges[nonceS] = &challenge{deviceID: deviceID, nonceC: nonceC, exp: now.Add(challengeTTL)}
	sig, err = s.sign(ChallengeMessage(deviceID, nonceC, nonceS))
	return nonceS, sig, err
}

// Auth finishes a sign-in: it verifies the client's signature and issues a
// bearer token valid for TokenTTL.
func (s *Store) Auth(deviceID, nonceS, sigB64, ip, via string) (token string, exp time.Time, role auth.Role, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	if s.limitedLocked(ip, now) {
		return "", time.Time{}, "", ErrRateLimit
	}
	s.pruneLocked(now)
	ch, ok := s.challenges[nonceS]
	if !ok || ch.deviceID != deviceID {
		s.failLocked(ip, now)
		return "", time.Time{}, "", ErrBadSig
	}
	delete(s.challenges, nonceS) // single use
	d := s.deviceLocked(deviceID)
	if d == nil {
		return "", time.Time{}, "", ErrUnknown
	}
	pub, err := parsePub(d.PubKey)
	if err != nil {
		return "", time.Time{}, "", ErrBadKey
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || !ecdsa.VerifyASN1(pub, digest(AuthMessage(deviceID, nonceS, ch.nonceC, s.fp)), sig) {
		s.failLocked(ip, now)
		return "", time.Time{}, "", ErrBadSig
	}
	token = randToken()
	exp = now.Add(TokenTTL)
	s.tokens[hashHex(token)] = &access{deviceID: deviceID, exp: exp}
	d.LastSeen, d.LastIP, d.Via = now, ip, via
	s.saveLocked()
	return token, exp, d.Role, nil
}

// CheckToken resolves a bearer token to its (still paired) device.
func (s *Store) CheckToken(token, ip, via string) (Device, bool) {
	if token == "" {
		return Device{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	a, ok := s.tokens[hashHex(token)]
	if !ok || !now.Before(a.exp) {
		return Device{}, false
	}
	d := s.deviceLocked(a.deviceID)
	if d == nil {
		return Device{}, false
	}
	d.LastSeen, d.LastIP, d.Via = now, ip, via
	return *d, true
}

// List returns paired devices, most recently seen first.
func (s *Store) List() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Device{}
	for _, d := range s.devices {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out
}

// Revoke unpairs a device; its tokens stop working immediately.
func (s *Store) Revoke(id string) bool {
	s.mu.Lock()
	var name string
	for i, d := range s.devices {
		if d.ID == id {
			name = d.Name
			s.devices = append(s.devices[:i], s.devices[i+1:]...)
			break
		}
	}
	if name != "" {
		for k, a := range s.tokens {
			if a.deviceID == id {
				delete(s.tokens, k)
			}
		}
		s.saveLocked()
	}
	s.mu.Unlock()
	if name != "" && s.OnEvent != nil {
		s.OnEvent("unpaired", name)
	}
	return name != ""
}

// SetRole changes a paired device's role; it applies to its next request.
func (s *Store) SetRole(id string, role auth.Role) bool {
	if role != auth.RoleAdmin && role != auth.RoleUser {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.deviceLocked(id)
	if d == nil {
		return false
	}
	d.Role = role
	s.saveLocked()
	return true
}

func (s *Store) deviceLocked(id string) *Device {
	for _, d := range s.devices {
		if d.ID == id {
			return d
		}
	}
	return nil
}

func (s *Store) pruneLocked(now time.Time) {
	for k, v := range s.invites {
		if !now.Before(v.exp) {
			delete(s.invites, k)
		}
	}
	for k, v := range s.challenges {
		if !now.Before(v.exp) {
			delete(s.challenges, k)
		}
	}
	for k, v := range s.tokens {
		if !now.Before(v.exp) {
			delete(s.tokens, k)
		}
	}
}

func (s *Store) limitedLocked(ip string, now time.Time) bool {
	kept := s.fails[ip][:0]
	for _, t := range s.fails[ip] {
		if now.Sub(t) < failWindow {
			kept = append(kept, t)
		}
	}
	s.fails[ip] = kept
	return len(kept) >= maxFailures
}

func (s *Store) failLocked(ip string, now time.Time) { s.fails[ip] = append(s.fails[ip], now) }

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.devices, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, "paired.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) sign(msg []byte) (string, error) {
	sig, err := ecdsa.SignASN1(rand.Reader, s.key, digest(msg))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// Verify checks an ECDSA signature made with the base64-DER public key.
func Verify(pubB64 string, msg []byte, sigB64 string) bool {
	pub, err := parsePub(pubB64)
	if err != nil {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	return err == nil && ecdsa.VerifyASN1(pub, digest(msg), sig)
}

// FingerprintOf returns the hex SHA-256 of a base64-DER public key.
func FingerprintOf(pubB64 string) (string, error) {
	der, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:]), nil
}

// FPEqual compares fingerprints in constant time.
func FPEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(a)), []byte(strings.ToLower(b))) == 1
}

func parsePub(b64 string) (*ecdsa.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	pub, ok := k.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, ErrBadKey
	}
	return pub, nil
}

func digest(msg []byte) []byte { sum := sha256.Sum256(msg); return sum[:] }

func hashHex(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }

func randToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func randID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func clean(s, def string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	if r := []rune(s); len(r) > 60 {
		s = string(r[:60])
	}
	return s
}
