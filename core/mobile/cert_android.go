//go:build android

package mobile

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"time"

	"tailscale.com/ipn/localapi"
	"tailscale.com/tsweb"
)

// tailscale.com/ipn/localapi/cert.go is built with !android (the official
// Android app gets certificates another way), so on Android the LocalAPI has
// no "cert/" endpoint and tsnet's Funnel/ListenTLS fail every TLS handshake
// with a 404. The ACME machinery itself (feature/acme) is still compiled in,
// so register the same endpoint here, mirroring upstream's serveCert.
func init() {
	localapi.Register("cert/", serveCert)
}

func serveCert(h *localapi.Handler, w http.ResponseWriter, r *http.Request) {
	if !h.PermitWrite && !h.PermitCert {
		http.Error(w, "cert access denied", http.StatusForbidden)
		return
	}
	domain, ok := strings.CutPrefix(r.URL.Path, "/localapi/v0/cert/")
	if !ok {
		http.Error(w, "internal handler config wired wrong", http.StatusInternalServerError)
		return
	}
	var minValidity time.Duration
	if v := r.URL.Query().Get("min_validity"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			http.Error(w, fmt.Sprintf("invalid validity parameter: %v", err), http.StatusBadRequest)
			return
		}
		minValidity = d
	}
	pair, err := h.LocalBackend().GetCertPEMWithValidity(r.Context(), domain, minValidity)
	if err != nil {
		var hs tsweb.HTTPStatuser
		if errors.As(err, &hs) {
			resp := hs.HTTPStatus()
			maps.Copy(w.Header(), resp.Header)
			http.Error(w, resp.Msg, resp.Code)
			return
		}
		http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	switch r.URL.Query().Get("type") {
	case "", "crt", "cert":
		w.Write(pair.CertPEM)
	case "key":
		w.Write(pair.KeyPEM)
	case "pair":
		w.Write(pair.KeyPEM)
		w.Write(pair.CertPEM)
	default:
		http.Error(w, `invalid type; want "cert" (default), "key", or "pair"`, http.StatusBadRequest)
	}
}
