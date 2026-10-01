package server

import (
	"fmt"
	"net/http"
	"strings"
)

// Supported UI languages. English is the default; Vietnamese is chosen when
// the visitor asked for it (cookie) or their browser prefers it.
const (
	langEN     = "en"
	langVI     = "vi"
	langCookie = "nasfone_lang"
)

// messages maps a key to its {English, Vietnamese} text. Texts may contain
// fmt verbs; JS-side texts use {0}, {1}… placeholders instead.
var messages = map[string][2]string{
	// login page
	"login_title":      {"Sign in – NASfone", "Đăng nhập – NASfone"},
	"login_sub":        {"Sign in to access your files", "Đăng nhập để truy cập dữ liệu"},
	"login_code":       {"6-digit code shown in the app", "Mã 6 số đang hiện trên app"},
	"login_code_hint":  {"Open the NASfone Server app to see the code. It changes every minute and works once.", "Mở app NASfone Server để xem mã. Mã tự đổi mỗi phút và chỉ dùng được 1 lần."},
	"login_name":       {"Device name (shown in the app)", "Tên thiết bị (hiện trong app)"},
	"login_btn":        {"Sign in", "Đăng nhập"},
	"login_session":    {"Your session ends when you close the browser (24 hours at most).", "Phiên đăng nhập kết thúc khi bạn đóng trình duyệt (tối đa 24 giờ)."},
	"login_ok":         {"Signed in…", "Đăng nhập thành công…"},
	"login_tries_left": {" {0} tries left.", " Còn {0} lần thử."},
	"err_status":       {"Error {0}", "Lỗi {0}"},
	"err_network":      {"Cannot reach the server.", "Không kết nối được tới server."},
	"browser":          {"Browser", "Trình duyệt"},
	"on":               {"on", "trên"},

	// login errors (server side)
	"err_bad_request": {"Invalid request.", "Yêu cầu không hợp lệ."},
	"err_code":        {"Wrong or expired code. Enter the code currently shown in the app.", "Mã không đúng hoặc đã hết hạn. Hãy nhập mã đang hiện trên app."},
	"err_code_wrong":  {"Wrong code.", "Mã không đúng."},
	"err_ip_locked":   {"Too many wrong attempts. Please wait 15 minutes.", "Thử sai quá nhiều lần. Vui lòng đợi 15 phút."},
	"err_global_lock": {"Sign-in is paused after too many wrong attempts. Please wait a few minutes.", "Đăng nhập tạm khóa do có quá nhiều lần thử sai. Vui lòng đợi vài phút."},
	"err_disabled":    {"Code sign-in is not enabled.", "Đăng nhập bằng mã chưa được bật."},
	"err_read_only":   {"This account can only view and download.", "Tài khoản chỉ có quyền xem và tải về."},

	// browse page
	"role_admin":      {"ADMIN", "ADMIN"},
	"role_user":       {"VIEW & DOWNLOAD ONLY", "CHỈ XEM & TẢI VỀ"},
	"logout":          {"Sign out", "Đăng xuất"},
	"upload_files":    {"⬆ Upload files", "⬆ Tải file lên"},
	"upload_folder":   {"⬆ Upload folder", "⬆ Tải thư mục lên"},
	"new_folder":      {"＋ New folder", "＋ Thư mục mới"},
	"connect_app":     {"🔗 Connect the app on this computer", "🔗 Kết nối app trên máy này"},
	"delete":          {"Delete", "Xóa"},
	"empty":           {"This folder is empty", "Thư mục trống"},
	"empty_drop":      {" — drag files here to upload", " — kéo thả file vào đây để tải lên"},
	"speed_test":      {"Speed test", "Đo tốc độ"},
	"speed_down_btn":  {"Download 100 MB", "Tải về 100 MB"},
	"speed_up_btn":    {"Upload 50 MB", "Tải lên 50 MB"},
	"drop_here":       {"Drop to upload", "Thả để tải lên"},
	"pair_title":      {"Connect the NASfone app on this computer", "Kết nối app NASfone trên máy này"},
	"pair_sub":        {"The app is paired with its own key pair and stays connected — no more codes.", "App sẽ được ghép đôi bằng cặp khóa riêng và kết nối liên tục, không cần nhập mã nữa."},
	"pair_admin":      {"Admin access", "Quyền Admin"},
	"pair_admin_d":    {"Full access: upload, overwrite, delete", "Toàn quyền: tải lên, ghi đè, xóa"},
	"pair_user":       {"User access", "Quyền User"},
	"pair_user_d":     {"View and download only", "Chỉ xem và tải về"},
	"pair_fallback":   {"App didn't open? Copy the invite and paste it into the app (valid 5 minutes, single use):", "Chưa mở được app? Sao chép lời mời rồi dán vào app (hết hạn sau 5 phút, dùng 1 lần):"},
	"pair_copy":       {"Copy invite", "Sao chép lời mời"},
	"close":           {"Close", "Đóng"},
	"dup_title":       {"File already exists", "File đã tồn tại"},
	"dup_body":        {"already exists in this folder. What do you want to do?", "đã có trong thư mục này. Bạn muốn làm gì?"},
	"dup_overwrite":   {"Replace with the new file", "Ghi đè bằng bản mới"},
	"dup_overwrite_d": {"The old file will be replaced", "File cũ sẽ bị thay thế"},
	"dup_keep":        {"Keep both", "Lưu thành bản thứ 2"},
	"dup_skip":        {"Skip", "Bỏ qua"},
	"dup_skip_d":      {"Keep the old file, don't upload this one", "Giữ nguyên file cũ, không tải file này lên"},

	// browse page, used from JavaScript
	"js_uploading":   {"Uploading {0}/{1}: {2} — {3}% ({4})", "Đang tải lên {0}/{1}: {2} — {3}% ({4})"},
	"js_conn_lost":   {"Connection lost: {0}", "Mất kết nối: {0}"},
	"js_mkdir_fail":  {"Could not create folder {0}", "Không tạo được thư mục {0}"},
	"js_dup_fail":    {"Could not check for duplicates ({0})", "Không kiểm tra được file trùng ({0})"},
	"js_keep_as":     {"Save as “{0}”", "Lưu với tên “{0}”"},
	"js_apply_all":   {"Apply to the remaining {0} duplicates", "Áp dụng cho {0} file trùng còn lại"},
	"js_checking":    {"Checking for duplicates…", "Đang kiểm tra file trùng…"},
	"js_new":         {"{0} new", "{0} file mới"},
	"js_overwritten": {"{0} replaced", "{0} ghi đè"},
	"js_kept":        {"{0} kept as copies", "{0} lưu bản thứ 2"},
	"js_skipped":     {"{0} skipped", "{0} bỏ qua"},
	"js_done":        {"✔ Done: {0}.", "✔ Xong: {0}."},
	"js_folder_name": {"New folder name:", "Tên thư mục mới:"},
	"js_error":       {"Error {0}", "Lỗi {0}"},
	"js_delete_q":    {"Delete “{0}”?", "Xóa “{0}”?"},
	"js_invite_fail": {"Could not create an invite ({0})", "Không tạo được lời mời ({0})"},
	"js_opening_app": {"Opening the NASfone app… ({0} access)", "Đang mở app NASfone… (quyền {0})"},
	"js_copied":      {"Invite copied", "Đã sao chép lời mời"},
	"js_copy_prompt": {"Copy the invite:", "Sao chép lời mời:"},
	"js_speed_down":  {"Download {0} MB… ", "Tải về {0} MB… "},
	"js_speed_up":    {"Upload {0} MB… ", "Tải lên {0} MB… "},
}

// pickLang chooses the UI language for a request.
func pickLang(r *http.Request) string {
	if c, err := r.Cookie(langCookie); err == nil && (c.Value == langVI || c.Value == langEN) {
		return c.Value
	}
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		switch {
		case strings.HasPrefix(tag, "vi"):
			return langVI
		case strings.HasPrefix(tag, "en"):
			return langEN
		}
	}
	return langEN
}

// tr returns the text for key in lang, formatted with args (fmt verbs).
func tr(lang, key string, args ...any) string {
	m, ok := messages[key]
	if !ok {
		return key
	}
	s := m[0]
	if lang == langVI {
		s = m[1]
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

// dict returns every message in lang, for templates ({{.T.key}}) and JS.
func dict(lang string) map[string]string {
	out := make(map[string]string, len(messages))
	for k := range messages {
		out[k] = tr(lang, k)
	}
	return out
}
