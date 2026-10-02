//go:build windows

package main

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// msgs maps a key to its {English, Vietnamese} text (fmt verbs allowed).
var msgs = map[string][2]string{
	// pairing dialogs
	"invite_bad":      {"This pairing invite is not valid.\n\n%s", "Lời mời ghép đôi không hợp lệ.\n\n%s"},
	"role_user_long":  {"USER — view and download only", "USER — chỉ xem và tải về"},
	"role_admin_long": {"ADMIN — full access (upload, overwrite, delete)", "ADMIN — toàn quyền (tải lên, ghi đè, xóa)"},
	"pair_confirm": {
		"Pair this computer with NASfone?\n\nServer: %s\nAccess: %s\nServer fingerprint: %s\n\nOnly agree if you just clicked \"Connect the app\" on your own NASfone page.",
		"Ghép máy tính này với NASfone?\n\nServer: %s\nQuyền: %s\nVân tay server: %s\n\nChỉ đồng ý nếu chính bạn vừa bấm \"Kết nối app\" trên trang NASfone của mình.",
	},
	"pair_replaces":   {"\n\nThis computer is already paired with this server — the new pairing replaces the old one.", "\n\nMáy này đã ghép với server này — ghép mới sẽ thay cho lần ghép cũ."},
	"pair_adds":       {"\n\nIt is added as another drive; the %d server(s) already paired stay connected.", "\n\nServer này được thêm thành một ổ đĩa nữa; %d server đã ghép vẫn giữ nguyên."},
	"key_fail":        {"Could not create a key: %s", "Không tạo được khóa: %s"},
	"pair_fail":       {"Pairing failed.\n\n%s", "Ghép đôi thất bại.\n\n%s"},
	"key_save_fail":   {"Could not save the key: %s", "Không lưu được khóa: %s"},
	"cfg_save_fail":   {"Could not save the settings: %s", "Không lưu được cấu hình: %s"},
	"pair_done":       {"Paired with %s (%s access).\n\nIt will appear in File Explorer as drive %s in a few seconds.", "Đã ghép đôi với %s (quyền %s).\n\nServer sẽ hiện thành ổ %s trong File Explorer sau vài giây."},
	"unpair_q":        {"Unpair from %s?\n\nIts drive is disconnected and this computer's key for it is deleted. Other servers are not affected. Also revoke this computer on that server.", "Hủy ghép đôi với %s?\n\nỔ đĩa của server này bị ngắt và khóa của máy này dành cho nó bị xóa. Các server khác không bị ảnh hưởng. Nên thu hồi thêm máy này trên server đó."},
	"already_running": {"NASfone is running — see its icon in the system tray (next to the clock).", "NASfone đang chạy — xem biểu tượng ở khay hệ thống (cạnh đồng hồ)."},
	"no_webview":      {"Cannot open the window. Microsoft Edge WebView2 Runtime is required.", "Không mở được cửa sổ. Máy cần Microsoft Edge WebView2 Runtime."},

	// drive / mount errors
	"no_rclone":      {"rclone.exe not found (put it next to NASfone.exe or install: winget install Rclone.Rclone)", "không tìm thấy rclone.exe (đặt cạnh NASfone.exe hoặc cài bằng: winget install Rclone.Rclone)"},
	"no_winfsp":      {"WinFsp is not installed (winget install WinFsp.WinFsp)", "chưa cài WinFsp (winget install WinFsp.WinFsp)"},
	"no_drive":       {"no free drive letter left", "không còn ký tự ổ đĩa trống"},
	"rclone_start":   {"could not start rclone: %v", "không chạy được rclone: %v"},
	"rclone_stopped": {"rclone stopped", "rclone đã dừng"},
	"drive_lost":     {"drive disconnected: %v (see %s)", "ổ đĩa bị ngắt: %v (xem %s)"},
	"not_paired":     {"not paired", "chưa ghép đôi"},

	// tray menu
	"m_window":        {"Open NASfone window", "Mở cửa sổ NASfone"},
	"m_starting":      {"Starting…", "Đang khởi động…"},
	"m_open":          {"Open NASfone drive", "Mở ổ NASfone"},
	"m_open_tip":      {"Open in File Explorer", "Mở trong File Explorer"},
	"m_web":           {"Open NASfone web page", "Mở trang web NASfone"},
	"m_pair":          {"Pair using the copied invite", "Ghép đôi bằng lời mời đã sao chép"},
	"m_pair_tip":      {"Paste a nasfone1:… invite from the clipboard", "Dán lời mời nasfone1:… từ clipboard"},
	"m_auto":          {"Start with Windows", "Khởi động cùng Windows"},
	"m_forget":        {"Unpair from this server", "Hủy ghép đôi với server này"},
	"m_web_many":      {"Open a NASfone web page…", "Mở trang web NASfone…"},
	"m_forget_many":   {"Manage servers…", "Quản lý server…"},
	"m_quit":          {"Quit", "Thoát"},
	"m_update":        {"⬆ Update to v%s", "⬆ Cập nhật lên v%s"},
	"m_updating":      {"Downloading v%s…", "Đang tải v%s…"},
	"update_fail":     {"Could not update NASfone: %s", "Không cập nhật được NASfone: %s"},
	"drive_not_ready": {"The NASfone drive is not ready.\n\n%s", "Ổ NASfone chưa sẵn sàng.\n\n%s"},
	"no_invite_clip":  {"The clipboard has no NASfone invite.\n\nOn the phone or the NASfone web page choose \"Pair new device\" → \"Copy invite\", then click this item again.", "Clipboard không có lời mời NASfone.\n\nTrên điện thoại hoặc trang web NASfone, chọn \"Ghép thiết bị\" → \"Sao chép lời mời\", rồi bấm lại mục này."},
	"autostart_fail":  {"Could not change the startup setting: %s", "Không đổi được cài đặt khởi động: %s"},

	// status lines
	"st_unpaired_hint": {"Not paired — click \"Connect the app\" on the NASfone web page", "Chưa ghép đôi — bấm \"Kết nối app\" trên trang web NASfone"},
	"st_role_user":     {"User (view only)", "User (chỉ xem)"},
	"st_role_admin":    {"Admin", "Admin"},
	"st_conn_err":      {"⚠ Cannot reach the server: %s", "⚠ Không kết nối được server: %s"},
	"st_ok":            {"✔ Connected • %s • drive %s", "✔ Đã kết nối • %s • ổ %s"},
	"st_multi":         {"%d/%d drives connected %s", "%d/%d ổ đã kết nối %s"},
	"s_ok_n":           {"%d servers connected", "Đã kết nối %d server"},
	"s_some_err":       {"%d of %d servers need attention", "%d/%d server đang gặp vấn đề"},
	"st_mounting":      {"Mounting the drive… • %s", "Đang gắn ổ đĩa… • %s"},
	"st_connecting":    {"Connecting… • %s", "Đang kết nối… • %s"},
	"s_unpaired":       {"Not paired", "Chưa ghép đôi"},
	"s_conn_err":       {"Cannot reach the server", "Không kết nối được server"},
	"s_ok":             {"Connected", "Đã kết nối"},
	"s_drive_err":      {"Drive error", "Ổ đĩa gặp lỗi"},
	"s_connecting":     {"Connecting…", "Đang kết nối…"},
	"s_revoked":        {"Revoked by the server", "Đã bị thu hồi"},
	"st_revoked":       {"⛔ This computer was revoked on the server — pair again", "⛔ Máy này đã bị thu hồi trên server — hãy ghép đôi lại"},
	"revoked_msg": {
		"This computer's access was revoked on the NASfone server %s, so its drive was disconnected.\n\nTo use it again, click \"Remove\" next to it in the NASfone window, then pair again from the server's web page.",
		"Quyền truy cập của máy này đã bị thu hồi trên NASfone server %s nên ổ đĩa của nó đã được ngắt.\n\nMuốn dùng lại, bấm \"Gỡ\" ở server đó trong cửa sổ NASfone rồi ghép đôi lại từ trang web của server.",
	},
	"w_disconnected":  {"Disconnected", "Đã ngắt"},
	"w_remove":        {"Remove", "Gỡ"},
	"w_servers":       {"Servers", "Server"},
	"w_add_server":    {"＋ Pair another server", "＋ Ghép thêm server"},
	"w_cancel":        {"Cancel", "Hủy"},
	"w_details":       {"Details", "Chi tiết"},
	"w_switch_server": {"Switch this PC to server", "Chuyển máy này sang làm server"},
	"w_switch_server_q": {"The drive is disconnected (the pairing is kept, you can switch back later) and NASfone restarts as a server that shares a folder of this PC. Continue?",
		"Ổ đĩa sẽ được ngắt (ghép đôi vẫn giữ, có thể chuyển về sau) và NASfone khởi động lại ở vai trò server, chia sẻ một thư mục của máy này. Tiếp tục?"},
	"role_q":           {"What is this PC for?", "Máy tính này dùng để làm gì?"},
	"role_server":      {"Be the server", "Làm server"},
	"role_server_desc": {"Share a folder of this PC. Other devices reach it through the web, the Windows app or NASfone on a phone.", "Chia sẻ một thư mục của máy này. Thiết bị khác truy cập qua web, app Windows hoặc NASfone trên điện thoại."},
	"role_client":      {"Be a client", "Làm client"},
	"role_client_desc": {"Use a NAS elsewhere (a phone or another PC) as a drive letter, e.g. P:.", "Dùng NAS ở máy khác (điện thoại hoặc PC khác) như một ổ đĩa, ví dụ P:."},
	"role_note":        {"You can change this later in Settings.", "Có thể đổi lại trong phần Cài đặt."},
	"role_fallback_q":  {"Use this PC as a NASfone server (share a folder)?\n\nYes = server, No = client (mount a NAS as a drive).", "Dùng máy này làm server NASfone (chia sẻ thư mục)?\n\nCó = server, Không = client (gắn NAS thành ổ đĩa)."},
	"link_on_server":   {"This PC runs NASfone as a server. To pair it as a client, switch the role in Settings first.", "Máy này đang chạy NASfone ở vai trò server. Muốn ghép đôi làm client, hãy đổi vai trò trong Cài đặt trước."},
	"w_update_avail":   {"NASfone v%s is available.", "Đã có NASfone v%s."},
	"w_update_btn":     {"Update", "Cập nhật"},
	"w_updating":       {"Downloading…", "Đang tải…"},

	// window (ui.html)
	"w_drive":         {"Drive", "Ổ đĩa"},
	"w_open_explorer": {"Open in File Explorer", "Mở trong File Explorer"},
	"w_web":           {"Web page", "Trang web"},
	"w_reconnect":     {"Reconnect", "Kết nối lại"},
	"w_connection":    {"Connection", "Kết nối"},
	"w_server":        {"Server", "Server"},
	"w_access":        {"Access", "Quyền"},
	"w_this_pc":       {"This computer", "Máy này"},
	"w_server_fp":     {"Server fingerprint", "Vân tay server"},
	"w_paired_at":     {"Paired at", "Ghép đôi lúc"},
	"w_pair_title":    {"Pair with NASfone", "Ghép đôi với NASfone"},
	"w_step1":         {"Open the NASfone web page on this computer and sign in with the <b>ADMIN code</b> shown on the phone.", "Mở trang web NASfone trên máy này và đăng nhập bằng <b>mã ADMIN</b> trên điện thoại."},
	"w_step2":         {"Click <b>🔗 Connect the app on this computer</b> and choose the access level.", "Bấm <b>🔗 Kết nối app trên máy này</b> và chọn quyền."},
	"w_step3":         {"Confirm in the dialog that appears — done.", "Xác nhận trong hộp thoại hiện ra — xong."},
	"w_or_paste":      {"Or paste a copied <span class=\"mono\">nasfone1:…</span> invite:", "Hoặc dán lời mời <span class=\"mono\">nasfone1:…</span> đã sao chép:"},
	"w_paste":         {"Paste from clipboard", "Dán từ clipboard"},
	"w_pair":          {"Pair", "Ghép đôi"},
	"w_pairing":       {"Pairing…", "Đang ghép đôi…"},
	"w_settings":      {"Settings", "Cài đặt"},
	"w_drive_letter":  {"Drive letter", "Ký tự ổ đĩa"},
	"w_autostart":     {"Start with Windows", "Khởi động cùng Windows"},
	"w_language":      {"Language", "Ngôn ngữ"},
	"w_logs":          {"Data & log folder", "Thư mục dữ liệu & log"},
	"w_unpair":        {"Unpair", "Hủy ghép đôi"},
	"w_missing":       {"Missing components to mount the drive: ", "Thiếu thành phần để gắn ổ đĩa: "},
	"w_role_admin":    {"ADMIN — full access", "ADMIN — toàn quyền"},
	"w_role_user":     {"USER — view & download only", "USER — chỉ xem & tải về"},
	"w_rw":            {"Read & write", "Đọc & ghi"},
	"w_ro":            {"Read only", "Chỉ đọc"},
	"w_drive_n":       {"drive", "ổ"},
	"w_not_mounted":   {"Drive not mounted", "Chưa gắn được ổ đĩa"},
	"w_mounting":      {"Mounting the drive…", "Đang gắn ổ đĩa…"},
}

var (
	kernel32UI               = windows.NewLazySystemDLL("kernel32.dll")
	procGetUserDefaultUILang = kernel32UI.NewProc("GetUserDefaultUILanguage")
)

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
