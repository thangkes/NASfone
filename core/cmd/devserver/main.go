// Command devserver runs the NASfone web handler on localhost for UI work,
// without Android or Tailscale. It prints the rolling admin/user login codes every minute.
//
//	go run ./cmd/devserver -root ./tmp-root
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"nasfone/core/auth"
	"nasfone/core/pair"
	"nasfone/core/server"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8787", "listen address (keep it on localhost)")
	invite := flag.String("invite", "", `print a one-time pairing invite for role "admin" or "user" at start`)
	root := flag.String("root", filepath.Join(os.TempDir(), "nasfone-dev-root"), "folder to serve")
	lan := flag.Bool("lan", false, "act as the LAN listener (QR sign-in); approve a QR with GET /__dev/approve-lan?qr=… on the -dev address")
	devAddr := flag.String("dev", "127.0.0.1:8788", "with -lan: address of the dev-only approve endpoint (stands in for the phone)")
	flag.Parse()

	os.MkdirAll(*root, 0o755)
	store, err := auth.Open(filepath.Join(*root, "..", "nasfone-dev-auth.json"))
	if err != nil {
		log.Fatal(err)
	}
	store.OnEvent = func(kind, detail string) { log.Printf("event %s: %s", kind, detail) }
	go func() {
		for {
			admin, user, exp := store.CurrentCodes()
			log.Printf("login codes: admin %s, user %s (until %s)", admin, user, exp.Format("15:04:05"))
			time.Sleep(time.Until(exp) + 100*time.Millisecond)
		}
	}()
	pairs, err := pair.Open(filepath.Join(*root, "..", "nasfone-dev-pair"))
	if err != nil {
		log.Fatal(err)
	}
	pairs.OnEvent = func(kind, detail string) { log.Printf("event %s: %s", kind, detail) }
	if *invite != "" {
		inv, _ := pairs.NewInvite(auth.Role(*invite), "http://"+*addr)
		log.Printf("invite (%s): %s", *invite, inv)
	}
	opt := server.Options{Root: *root, Auth: store, Pair: pairs, Logf: log.Printf}
	via := "Dev"
	if *lan {
		opt.LAN = server.NewLAN()
		opt.LAN.Logf = log.Printf
		via = server.ViaLAN
		go func() {
			// Plays the phone's part: approve the QR text a browser shows.
			mux := http.NewServeMux()
			mux.HandleFunc("/__dev/approve-lan", func(w http.ResponseWriter, r *http.Request) {
				ip, agent, err := opt.LAN.TicketInfo(r.URL.Query().Get("qr"))
				if err == nil {
					_, err = opt.LAN.Approve(r.URL.Query().Get("qr"))
				}
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				fmt.Fprintf(w, "approved %s (%s)\n", ip, agent)
			})
			log.Printf("LAN approve endpoint on http://%s/__dev/approve-lan?qr=…", *devAddr)
			log.Fatal(http.ListenAndServe(*devAddr, mux))
		}()
	}
	h := server.WithVia(server.NewHandler(opt), via)
	log.Printf("serving %s on http://%s", *root, *addr)
	log.Fatal(http.ListenAndServe(*addr, h))
}
