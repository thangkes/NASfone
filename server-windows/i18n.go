//go:build windows

package main

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// msgs maps a key to {English, Vietnamese}. Keys starting with "w_" are sent
// to the window (uiDict); the rest are used from Go.
var msgs = map[string][2]string{
	// Tray and dialogs.
	"m_window":            {"Open NASfone Server", "Mở NASfone Server"},
	"m_folder":            {"Open the shared folder", "Mở thư mục chia sẻ"},
	"m_web":               {"Open the web page", "Mở trang web"},
	"m_auto":              {"Start with Windows", "Khởi động cùng Windows"},
	"m_quit":              {"Stop and quit", "Dừng và thoát"},
	"m_update":            {"⬆ Update to v%s", "⬆ Cập nhật lên v%s"},
	"st_stopped":          {"○ Stopped", "○ Đã dừng"},
	"st_starting":         {"● Starting…", "● Đang khởi động…"},
	"st_running":          {"● Running • tailnet connected", "● Đang chạy • tailnet đã kết nối"},
	"st_login":            {"● Running • waiting for Tailscale sign-in", "● Đang chạy • chờ đăng nhập Tailscale"},
	"st_error":            {"⛔ Error: %s", "⛔ Lỗi: %s"},
	"already_running":     {"NASfone Server is already running (see the tray icon).", "NASfone Server đang chạy (xem biểu tượng ở khay hệ thống)."},
	"no_webview":          {"Cannot open the window: Microsoft Edge WebView2 Runtime is missing.", "Không mở được cửa sổ: thiếu Microsoft Edge WebView2 Runtime."},
	"autostart_fail":      {"Could not change the startup setting: %s", "Không đổi được cài đặt khởi động: %s"},
	"update_fail":         {"Could not update NASfone Server: %s", "Không cập nhật được NASfone Server: %s"},
	"pick_folder":         {"Choose the folder to share", "Chọn thư mục để chia sẻ"},
	"lan_port_bad":        {"The LAN port must be between 1024 and 65535.", "Cổng LAN phải từ 1024 đến 65535."},
	"lan_open_fail":       {"Could not open the LAN port: %s", "Không mở được cổng LAN: %s"},
	"host_bad":            {"The machine name may only use a-z, 0-9 and - (up to 63 characters).", "Tên máy chỉ gồm a-z, 0-9 và dấu - (tối đa 63 ký tự)."},
	"control_bad":         {"The control server must start with https://", "Máy chủ điều khiển phải bắt đầu bằng https://"},
	"folder_bad":          {"Choose a full folder path, e.g. D:\\NASfone.", "Hãy chọn đường dẫn thư mục đầy đủ, ví dụ D:\\NASfone."},
	"folder_fail":         {"Cannot use this folder: %s", "Không dùng được thư mục này: %s"},
	"log_start_fail":      {"Could not start: %s", "Không khởi động được: %s"},
	"log_control_changed": {"Control server changed: sign in again.", "Đã đổi máy chủ điều khiển: cần đăng nhập lại."},

	// Window.
	"w_setup_title":   {"First-time setup", "Thiết lập lần đầu"},
	"w_setup_intro":   {"Choose the folder this PC shares. Everything inside it can be reached from your paired apps and browsers.", "Chọn thư mục mà máy này chia sẻ. Mọi thứ bên trong sẽ truy cập được từ các app đã ghép và trình duyệt."},
	"w_folder":        {"Shared folder", "Thư mục chia sẻ"},
	"w_choose":        {"Choose…", "Chọn…"},
	"w_hostname":      {"Machine name in the tailnet", "Tên máy trong tailnet"},
	"w_start":         {"Start", "Bắt đầu"},
	"w_update_avail":  {"NASfone Server v%s is available.", "Đã có NASfone Server v%s."},
	"w_update_btn":    {"Update", "Cập nhật"},
	"w_updating":      {"Downloading…", "Đang tải…"},
	"w_stop":          {"Stop server", "Dừng server"},
	"w_start_server":  {"Start server", "Khởi động server"},
	"w_open_web":      {"Open web page", "Mở trang web"},
	"w_open_folder":   {"Open shared folder", "Mở thư mục chia sẻ"},
	"w_lan_requests":  {"Browsers asking to sign in over the LAN", "Trình duyệt xin vào qua LAN"},
	"w_lan_req_hint":  {"Allow only if the check code matches the one on that browser's page. Access: view and download only.", "Chỉ cho phép khi mã đối chiếu khớp với mã trên trang của trình duyệt đó. Quyền: chỉ xem và tải về."},
	"w_allow":         {"Allow", "Cho phép"},
	"w_deny":          {"Deny", "Từ chối"},
	"w_codes_title":   {"Web sign-in codes (6 digits, change every minute)", "Mã đăng nhập web (6 số, đổi mỗi phút)"},
	"w_admin_label":   {"ADMIN — full access (upload, overwrite, delete)", "ADMIN — toàn quyền (tải lên, ghi đè, xoá)"},
	"w_user_label":    {"USER — view & download only", "USER — chỉ xem & tải về"},
	"w_copy":          {"Copy", "Sao chép"},
	"w_copied":        {"Copied", "Đã chép"},
	"w_codes_next":    {"New codes in %s • each code works once", "Đổi mã sau %s • mỗi mã dùng 1 lần"},
	"w_account":       {"Tailscale account", "Tài khoản Tailscale"},
	"w_signed_in_as":  {"Signed in as", "Đã đăng nhập"},
	"w_tailnet":       {"Tailnet", "Tailnet"},
	"w_not_signed":    {"Not signed in. Click \"Sign in\" to open the Tailscale sign-in page.", "Chưa đăng nhập. Bấm \"Đăng nhập\" để mở trang đăng nhập Tailscale."},
	"w_sign_in":       {"Sign in", "Đăng nhập"},
	"w_sign_out":      {"Sign out / switch account", "Đăng xuất / đổi tài khoản"},
	"w_sign_out_q":    {"The server leaves the current tailnet. Then click \"Sign in\" to use another account. Continue?", "Server sẽ rời tailnet hiện tại. Sau đó bấm \"Đăng nhập\" để dùng tài khoản khác. Tiếp tục?"},
	"w_addresses":     {"Addresses", "Địa chỉ truy cập"},
	"w_addr_funnel":   {"Funnel (public)", "Funnel (công khai)"},
	"w_addr_lan":      {"LAN (same network)", "LAN (cùng mạng)"},
	"w_funnel":        {"Funnel (public access over the internet)", "Funnel (truy cập công khai qua Internet)"},
	"w_funnel_on":     {"Enable Funnel", "Bật Funnel"},
	"w_funnel_off":    {"Off — reachable only through the tailnet.", "Tắt — chỉ truy cập qua tailnet."},
	"w_funnel_wait":   {"Opens once the tailnet is connected.", "Sẽ mở khi tailnet kết nối xong."},
	"w_funnel_public": {"Public at:", "Đang mở công khai:"},
	"w_funnel_retry":  {"The app retries every 20 seconds after you fix it.", "App tự thử lại mỗi 20 giây sau khi bạn sửa."},
	"w_funnel_fix":    {"Open the Tailscale settings page to fix this", "Mở trang cài đặt Tailscale để sửa"},
	"w_lan":           {"LAN access (same network, no internet needed)", "Kết nối LAN (cùng mạng, không cần internet)"},
	"w_lan_port":      {"LAN port", "Cổng LAN"},
	"w_lan_on":        {"Enable LAN access", "Bật kết nối LAN"},
	"w_lan_hint":      {"Browsers on the same network open the LAN address and sign in with a 6-digit code, or show a QR code that you approve here (view only). Plain HTTP: use it on networks you trust. To change the port, switch off and on.", "Trình duyệt cùng mạng mở địa chỉ LAN, đăng nhập bằng mã 6 số, hoặc hiện mã QR để bạn cho phép tại đây (chỉ xem). HTTP thường: chỉ dùng ở mạng tin cậy. Đổi cổng: tắt rồi bật lại."},
	"w_paired":        {"Paired apps (Windows / Android)", "Ứng dụng đã ghép (Windows / Android)"},
	"w_none":          {"None yet.", "Chưa có."},
	"w_pair_new":      {"＋ Pair a new device", "＋ Ghép thiết bị mới"},
	"w_make_user":     {"Make User", "Hạ xuống User"},
	"w_make_admin":    {"Make Admin", "Nâng lên Admin"},
	"w_revoke":        {"Revoke", "Thu hồi"},
	"w_revoke_q":      {"Revoke this device? It loses access at once and must pair again.", "Thu hồi thiết bị này? Nó mất quyền truy cập ngay và phải ghép lại."},
	"w_key":           {"key", "khoá"},
	"w_last_seen":     {"last seen", "lần cuối"},
	"w_paired_at":     {"paired", "ghép lúc"},
	"w_via":           {"via", "qua"},
	"w_invite_title":  {"Pair a new device", "Ghép thiết bị mới"},
	"w_invite_role_q": {"Which access does the new device get?", "Thiết bị mới được quyền gì?"},
	"w_admin":         {"Admin access", "Quyền Admin"},
	"w_user":          {"User access", "Quyền User"},
	"w_invite_hint":   {"On the new device: Android app → scan this QR code; Windows client → copy the invite and paste it in the NASfone window. Valid for 5 minutes, works once.", "Trên thiết bị mới: app Android → quét mã QR này; app Windows → sao chép lời mời rồi dán vào cửa sổ NASfone. Dùng được 5 phút, một lần."},
	"w_copy_invite":   {"Copy invite", "Sao chép lời mời"},
	"w_close":         {"Close", "Đóng"},
	"w_fp":            {"Server key fingerprint", "Mã nhận dạng server"},
	"w_sessions":      {"Browser sessions", "Phiên trình duyệt"},
	"w_revoke_all":    {"Sign out all browsers", "Đăng xuất mọi trình duyệt"},
	"w_revoke_all_q":  {"Sign out every browser session?", "Đăng xuất mọi phiên trình duyệt?"},
	"w_expires":       {"expires", "hết hạn"},
	"w_settings":      {"Settings", "Cài đặt"},
	"w_control":       {"Control server (empty = Tailscale; a URL for Headscale)", "Máy chủ điều khiển (để trống = Tailscale; điền URL nếu dùng Headscale)"},
	"w_verbose":       {"Verbose Tailscale logs", "Ghi log chi tiết của Tailscale"},
	"w_save":          {"Save settings", "Lưu cài đặt"},
	"w_saved":         {"Saved", "Đã lưu"},
	"w_save_note":     {"Changing the folder, name or control server restarts the server.", "Đổi thư mục, tên máy hoặc máy chủ điều khiển sẽ khởi động lại server."},
	"w_autostart":     {"Start with Windows", "Khởi động cùng Windows"},
	"w_language":      {"Language", "Ngôn ngữ"},
	"w_lang_auto":     {"Follow Windows", "Theo Windows"},
	"w_log":           {"Log", "Nhật ký"},
	"w_logs_folder":   {"Data & log folder", "Thư mục dữ liệu & log"},
	"w_copy_log":      {"Copy log", "Sao chép nhật ký"},
	"w_stopped_hint":  {"The server is stopped.", "Server đang dừng."},
}

var procGetUserDefaultUILang = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserDefaultUILanguage")

// lang is "vi" or "en": the in-app choice first, then the Windows display
// language, English otherwise.
func lang() string {
	if l := getSettings().Lang; l == "vi" || l == "en" {
		return l
	}
	id, _, _ := procGetUserDefaultUILang.Call()
	if id&0x3ff == 0x2a { // LANG_VIETNAMESE
		return "vi"
	}
	return "en"
}

// t returns the localized text for key, formatted with args.
func t(key string, args ...any) string {
	m, ok := msgs[key]
	if !ok {
		return key
	}
	s := m[0]
	if lang() == "vi" {
		s = m[1]
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

// uiDict returns the window texts (keys starting with "w_") in the current language.
func uiDict() map[string]string {
	out := map[string]string{}
	for k := range msgs {
		if strings.HasPrefix(k, "w_") {
			out[k] = t(k)
		}
	}
	return out
}
