package mobile

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"nasfone/core/update"
)

func isServerAPK(name string) bool {
	return strings.HasPrefix(name, "NASfone-Server-") && strings.HasSuffix(name, ".apk")
}

// CheckUpdate returns a JSON release {version, notes, pageURL, assetName, ...}
// newer than current that ships a server APK, or "" when up to date.
func CheckUpdate(current string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := update.Check(ctx, http.DefaultClient, current, isServerAPK)
	if err != nil || r == nil {
		return "", err
	}
	b, _ := json.Marshal(r)
	return string(b), nil
}

// DownloadUpdate saves the APK of a release (JSON from CheckUpdate) to dest,
// verified against the release's published SHA-256.
func DownloadUpdate(releaseJSON, dest string) error {
	var r update.Release
	if err := json.Unmarshal([]byte(releaseJSON), &r); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	return update.Download(ctx, http.DefaultClient, &r, dest)
}
