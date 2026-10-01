// Command devserver runs the PocketNAS web handler on localhost for UI work,
// without Android or Tailscale. It prints the rolling login code every minute.
//
//	go run ./cmd/devserver -root ./tmp-root
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"pocketnas/core/auth"
	"pocketnas/core/server"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8787", "listen address (keep it on localhost)")
	root := flag.String("root", filepath.Join(os.TempDir(), "pnas-dev-root"), "folder to serve")
	flag.Parse()

	os.MkdirAll(*root, 0o755)
	store, err := auth.Open(filepath.Join(*root, "..", "pnas-dev-auth.json"))
	if err != nil {
		log.Fatal(err)
	}
	store.OnEvent = func(kind, detail string) { log.Printf("event %s: %s", kind, detail) }
	go func() {
		for {
			code, exp := store.CurrentCode()
			log.Printf("login code %s (until %s)", code, exp.Format("15:04:05"))
			time.Sleep(time.Until(exp) + 100*time.Millisecond)
		}
	}()
	h := server.WithVia(server.NewHandler(server.Options{Root: *root, Auth: store, Logf: log.Printf}), "Dev")
	log.Printf("serving %s on http://%s", *root, *addr)
	log.Fatal(http.ListenAndServe(*addr, h))
}
