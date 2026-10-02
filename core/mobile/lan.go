package mobile

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"nasfone/core/auth"
	"nasfone/core/server"
)

// setLAN opens the local-network listener (plain HTTP on port) used by
// browsers that sign in with a 6-digit code or a QR code scanned by the phone,
// or closes it when port is 0. It works without internet, e.g. over the
// phone's own hotspot. Closing it ends every LAN session.
func (n *node) setLAN(port int) error {
	n.mu.Lock()
	old, oldPort := n.lanSrv, n.lanPort
	n.lanSrv, n.lanPort = nil, 0
	n.lanIPs, n.lanIPsSet = "", false
	n.status.LANURLs, n.status.LANError = nil, ""
	n.mu.Unlock()
	if old != nil {
		old.Close()
		if k := n.lan.RevokeAll(); k > 0 {
			n.logf("Đã tắt cổng LAN %d, kết thúc %d phiên LAN", oldPort, k)
		} else {
			n.logf("Đã tắt cổng LAN %d", oldPort)
		}
	}
	if port <= 0 {
		n.publish()
		return nil
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		n.mu.Lock()
		n.status.LANError = err.Error()
		n.mu.Unlock()
		n.publish()
		n.logf("Không mở được cổng LAN %d: %v", port, err)
		return err
	}
	srv := n.serve(ln, server.ViaLAN)
	n.mu.Lock()
	n.lanSrv, n.lanPort = srv, port
	n.mu.Unlock()
	n.logf("Kết nối LAN: đang nghe cổng %d", port)
	n.checkLAN()
	return nil
}

// SetLan switches the LAN listener on (port > 0) or off (0) while running.
func SetLan(port int) error {
	n := get()
	if n == nil {
		return nil // applied at the next start from the saved setting
	}
	return n.setLAN(port)
}

// localIPv4s lists the phone's own private IPv4 addresses (Wi-Fi, hotspot,
// Ethernet), leaving out loopback and Tailscale's 100.64.0.0/10 range.
func localIPv4s() []string {
	h := hostRef
	if h == nil {
		return nil
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	var out []string
	for _, line := range strings.Split(h.Interfaces(), "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|")
		if len(parts) != 5 || !strings.Contains(parts[3], "u") || strings.Contains(parts[3], "l") {
			continue
		}
		for _, a := range strings.Split(parts[4], ",") {
			ip, _, err := net.ParseCIDR(a)
			if err != nil || ip.To4() == nil || !ip.IsPrivate() || cgnat.Contains(ip) {
				continue
			}
			out = append(out, ip.String())
		}
	}
	sort.Strings(out)
	return out
}

// checkLAN publishes the LAN addresses and ends every LAN session when the
// phone's local IP changes (new Wi-Fi, hotspot toggled, DHCP renewal).
func (n *node) checkLAN() {
	n.mu.Lock()
	port := n.lanPort
	n.mu.Unlock()
	if port <= 0 || n.lan == nil {
		return
	}
	ips := localIPv4s()
	key := strings.Join(ips, ",")
	n.mu.Lock()
	if n.lanPort != port { // switched off or moved meanwhile
		n.mu.Unlock()
		return
	}
	prev, first := n.lanIPs, !n.lanIPsSet
	n.lanIPs, n.lanIPsSet = key, true
	n.status.LANURLs = n.status.LANURLs[:0]
	for _, ip := range ips {
		n.status.LANURLs = append(n.status.LANURLs, fmt.Sprintf("http://%s:%d", ip, port))
	}
	n.mu.Unlock()
	if !first && key != prev {
		if k := n.lan.RevokeAll(); k > 0 {
			n.logf("IP LAN đổi (%s → %s): đã kết thúc %d phiên LAN", prev, key, k)
		}
	}
	n.publish()
}

// LanTicketInfo checks a scanned QR code and returns {"ip","agent"} of the
// browser asking, for the confirmation dialog.
func LanTicketInfo(qrText string) (string, error) {
	n := get()
	if n == nil || n.lanOff() {
		return "", fmt.Errorf("LAN is off")
	}
	ip, agent, err := n.lan.TicketInfo(qrText)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(map[string]string{"ip": ip, "agent": agent})
	return string(b), nil
}

// ApproveLan lets the browser that shows this QR code in (read-only, LAN only).
func ApproveLan(qrText string) error {
	n := get()
	if n == nil || n.lanOff() {
		return fmt.Errorf("LAN is off")
	}
	_, err := n.lan.Approve(qrText)
	return err
}

func (n *node) lanOff() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.lanSrv == nil
}

// LanPending lists browsers waiting for LAN approval as JSON
// [{"ticket","short","ip","agent","created"}] (for servers without a camera).
func LanPending() string {
	n := get()
	if n == nil || n.lanOff() {
		return "[]"
	}
	b, _ := json.Marshal(n.lan.Pending())
	return string(b)
}

// ApproveLanTicket approves a waiting browser by ticket (from LanPending).
func ApproveLanTicket(ticket string) error {
	return ApproveLan(server.LANPrefix + ticket)
}

// RejectLanTicket drops a waiting browser's request.
func RejectLanTicket(ticket string) {
	if n := get(); n != nil {
		n.lan.Reject(ticket)
	}
}

// IsLanQR reports whether scanned text is a NASfone LAN sign-in code.
func IsLanQR(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), server.LANPrefix)
}

// lanDevices shows LAN sessions in the browser-session list (id "lan:…").
func (n *node) lanDevices() []auth.Device {
	if n.lan == nil {
		return nil
	}
	var out []auth.Device
	for _, s := range n.lan.Sessions() {
		name := "LAN"
		if a := shortAgent(s.Agent); a != "" {
			name += " · " + a
		}
		out = append(out, auth.Device{
			ID: "lan:" + s.ID, Name: name, Role: auth.RoleUser, Via: "LAN (QR)",
			LastIP: s.IP, Created: s.Created, LastSeen: s.LastSeen, Expires: s.Expires,
		})
	}
	return out
}

// shortAgent turns a user agent into "Firefox on Windows".
func shortAgent(ua string) string {
	br := ""
	switch {
	case strings.Contains(ua, "Edg/"):
		br = "Edge"
	case strings.Contains(ua, "Firefox/"):
		br = "Firefox"
	case strings.Contains(ua, "Chrome/"):
		br = "Chrome"
	case strings.Contains(ua, "Safari/"):
		br = "Safari"
	}
	os := ""
	switch {
	case strings.Contains(ua, "Windows"):
		os = "Windows"
	case strings.Contains(ua, "Android"):
		os = "Android"
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"):
		os = "iOS"
	case strings.Contains(ua, "Mac OS"):
		os = "macOS"
	case strings.Contains(ua, "Linux"):
		os = "Linux"
	}
	switch {
	case br != "" && os != "":
		return br + " / " + os
	case br != "":
		return br
	}
	return os
}
