//go:build windows

package main

import (
	"context"
	"net/url"
	"strings"
	"time"

	"nasfone/core/client"
)

// upgradeURL switches a paired http://*.ts.net address to https:// when the
// server answers there. Plain http only works inside the tailnet, while the
// https (Funnel) address also works from anywhere on the internet. The
// server identity is checked by the key-pair sign-in, so trying the other
// scheme is safe. It returns the (possibly updated) config and whether it changed.
func upgradeURL(cfg client.Config) (client.Config, bool) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme != "http" || !strings.HasSuffix(u.Hostname(), ".ts.net") {
		return cfg, false
	}
	key, err := loadKey()
	if err != nil {
		return cfg, false
	}
	try := cfg
	u.Scheme, u.Host = "https", u.Hostname()
	try.URL = u.String()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := client.Authenticate(ctx, httpClient, try, key); err != nil {
		return cfg, false
	}
	if saveConfig(try) != nil {
		return cfg, false
	}
	return try, true
}
