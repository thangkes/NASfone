package mobile

import (
	"errors"
	"strings"
	"testing"
)

func TestExplainFunnelError(t *testing.T) {
	for _, tc := range []struct{ in, wantMsg, wantURL string }{
		{"Funnel not available; HTTPS must be enabled. See https://tailscale.com/s/https.", "HTTPS Certificates", "/admin/dns"},
		{`Funnel not available; "funnel" node attribute not set. See https://tailscale.com/s/no-funnel.`, "quyền Funnel", "/admin/acls/file"},
		{"port 443 is not allowed for funnel", "Cổng 443", "/admin/acls/file"},
		{"something else", "something else", ""},
	} {
		msg, url := explainFunnelError(errors.New(tc.in))
		if !strings.Contains(msg, tc.wantMsg) || !strings.HasSuffix(url, tc.wantURL) {
			t.Errorf("%q -> %q %q", tc.in, msg, url)
		}
	}
}
