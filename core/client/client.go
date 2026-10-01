// Package client is the client side of NASfone pairing, shared by the
// Windows app and (later) the Android client app.
package client

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"nasfone/core/auth"
	"nasfone/core/pair"
)

// Config is what a client remembers about the server after pairing. None of
// it is secret; the private key lives elsewhere (DPAPI/TPM/Keystore).
type Config struct {
	URL       string    `json:"url"`
	ServerKey string    `json:"serverKey"` // pinned server public key, base64 DER
	ServerFP  string    `json:"serverFP"`
	DeviceID  string    `json:"deviceId"`
	Role      auth.Role `json:"role"`
	Name      string    `json:"name"`
	PairedAt  time.Time `json:"pairedAt"`
}

// GenerateKey makes a new ECDSA P-256 key pair.
func GenerateKey() (*ecdsa.PrivateKey, error) { return ecdsa.GenerateKey(elliptic.P256(), rand.Reader) }

// PublicKeyB64 returns the signer's public key as base64 DER SPKI.
func PublicKeyB64(s crypto.Signer) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(s.Public())
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(der), nil
}

func signB64(s crypto.Signer, msg []byte) (string, error) {
	sum := sha256.Sum256(msg)
	sig, err := s.Sign(rand.Reader, sum[:], crypto.SHA256) // ECDSA signers return ASN.1
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

func nonce() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Pair redeems an invite string with the signer's public key and verifies
// that the answering server holds the key the invite promised.
func Pair(ctx context.Context, hc *http.Client, inviteStr string, signer crypto.Signer, name, platform string) (Config, error) {
	inv, err := pair.ParseInvite(inviteStr)
	if err != nil {
		return Config{}, err
	}
	pub, err := PublicKeyB64(signer)
	if err != nil {
		return Config{}, err
	}
	nc := nonce()
	var resp pair.PairResponse
	if err := postJSON(ctx, hc, inv.URL+"/__nasfone/pair", pair.PairRequest{
		Token: inv.Token, PubKey: pub, Name: name, Platform: platform, NonceC: nc,
	}, &resp); err != nil {
		return Config{}, err
	}
	fp, err := pair.FingerprintOf(resp.ServerKey)
	if err != nil || !pair.FPEqual(fp, inv.FP) {
		return Config{}, errors.New("server trả lời không đúng khóa trong lời mời — có thể là server giả mạo")
	}
	if !pair.Verify(resp.ServerKey, pair.PairMessage(inv.Token, pub, resp.DeviceID, nc), resp.Sig) {
		return Config{}, errors.New("chữ ký của server không hợp lệ")
	}
	return Config{
		URL: inv.URL, ServerKey: resp.ServerKey, ServerFP: fp,
		DeviceID: resp.DeviceID, Role: resp.Role, Name: name, PairedAt: time.Now(),
	}, nil
}

// Token is a short-lived bearer token.
type Token struct {
	Value   string
	Expires time.Time
	Role    auth.Role
}

// Authenticate proves both identities (server first) and returns a bearer token.
func Authenticate(ctx context.Context, hc *http.Client, cfg Config, signer crypto.Signer) (Token, error) {
	nc := nonce()
	var ch struct {
		NonceS string `json:"nonceS"`
		Sig    string `json:"sig"`
	}
	if err := postJSON(ctx, hc, cfg.URL+"/__nasfone/auth/challenge", map[string]string{"deviceId": cfg.DeviceID, "nonceC": nc}, &ch); err != nil {
		return Token{}, err
	}
	if !pair.Verify(cfg.ServerKey, pair.ChallengeMessage(cfg.DeviceID, nc, ch.NonceS), ch.Sig) {
		return Token{}, errors.New("server không chứng minh được danh tính (khóa không khớp với lúc ghép đôi)")
	}
	sig, err := signB64(signer, pair.AuthMessage(cfg.DeviceID, ch.NonceS, nc, cfg.ServerFP))
	if err != nil {
		return Token{}, err
	}
	var tok struct {
		Token     string    `json:"token"`
		ExpiresIn int       `json:"expiresIn"`
		Role      auth.Role `json:"role"`
	}
	if err := postJSON(ctx, hc, cfg.URL+"/__nasfone/auth", map[string]string{"deviceId": cfg.DeviceID, "nonceS": ch.NonceS, "sig": sig}, &tok); err != nil {
		return Token{}, err
	}
	return Token{Value: tok.Token, Expires: time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second), Role: tok.Role}, nil
}

// HTTPError carries the server's status and message.
type HTTPError struct {
	Status int
	Msg    string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("server %d: %s", e.Status, e.Msg) }

func postJSON(ctx context.Context, hc *http.Client, url string, in, out any) error {
	b, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(body))
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		return &HTTPError{Status: res.StatusCode, Msg: msg}
	}
	return json.Unmarshal(body, out)
}
