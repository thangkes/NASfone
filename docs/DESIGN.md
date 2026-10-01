# PocketNAS – Thiết kế

Tài liệu này tổng hợp các quyết định đã thống nhất. Những mục **[Chưa chốt]** đang chờ quyết định.

## 1. Mục tiêu

- Điện thoại phụ (Honor 400 Lite, không root) làm **server lưu trữ di động**.
- Truy cập được bằng **mạng nội bộ**, **hotspot của chính server**, **USB tethering**, hoặc **Internet** (chỉ cần có mạng, không cần cấu hình router).
- Mỗi thiết bị client **ghép đôi bằng cặp khóa riêng**. Server ghi nhớ public key của client, client ghi nhớ public key của server.
- Có thể phát hành cho người khác dùng: mỗi người dùng tài khoản Tailscale của chính họ, không cần server trung gian của dự án.

## 2. Kiến trúc

| Thành phần | Vai trò |
|---|---|
| `core` (Go) | tsnet (nút Tailscale nhúng trong app, chạy userspace, không chiếm VPN của Android), WebDAV (`golang.org/x/net/webdav`), HTTP + giao diện web, giao thức ghép đôi và xác thực |
| `server-android` | Foreground service (`specialUse`), wake lock + Wi-Fi lock, tự chạy khi khởi động máy, chọn nơi lưu trữ (bộ nhớ trong / USB OTG), quản lý thiết bị tin cậy, giữ khóa server trong Android Keystore |
| `client-android` | Ghép đôi bằng QR, duyệt file, DocumentsProvider (hiện trong app Files và mọi hộp chọn file), tự backup ảnh |
| `client-windows` | EXE: ghép đôi, gắn ổ `P:\` (WinFsp), biểu tượng khay hệ thống, tự chọn đường LAN hoặc Internet, chạy cùng Windows |

## 3. Đường kết nối

| Đường | Địa chỉ | Ghi chú |
|---|---|---|
| Tailnet | `https://pocketnas.<tailnet>.ts.net` | Kết nối thẳng P2P. Máy client phải ở trong cùng tailnet |
| Funnel | `https://pocketnas.<tailnet>.ts.net` (công khai) | Chỉ cần Internet. Đi qua relay của Tailscale nên chậm hơn |

Client tự chọn theo thứ tự: **Tailnet → Funnel**.

> **Đã bỏ chế độ LAN** (2026-10-01): server không mở cổng nào ngoài mạng Tailscale. Tailnet đã tự kết nối thẳng P2P khi hai máy ở gần nhau, nên vẫn nhanh. Đổi lại không còn chế độ hotspot offline (Tailscale cần Internet để kết nối ban đầu).

## 4. Định danh và xác thực

### 4.1 Khóa danh tính
- **Server:** cặp khóa Ed25519/ECDSA trong Android Keystore (TEE), không xuất ra được.
- **Client Android:** Android Keystore (TEE/StrongBox).
- **Client Windows:** TPM qua CNG; nếu máy không có TPM thì dùng DPAPI.
- **iPhone / trình duyệt:** passkey (WebAuthn), chỉ dùng được trên HTTPS.

Thông số máy (tên máy, User-Agent, IP) **chỉ dùng để hiển thị và cảnh báo bất thường**, không dùng làm định danh.

### 4.2 Ghép đôi
1. Server tạo **chuỗi ghép đôi dùng 1 lần** (hạn 5 phút), gồm địa chỉ, vân tay public key của server và token. Hiển thị dạng QR kèm nút sao chép.
2. Client kiểm tra vân tay server, rồi gửi `token + public key client + tên thiết bị`.
3. Server lưu public key client vào danh sách tin cậy, hủy token.
4. Mỗi lần kết nối sau đó, hai bên ký thử thách (challenge-response) ở tầng ứng dụng, nên hoạt động giống nhau trên mọi đường kết nối.

### 4.3 Mức tin cậy
| Mức | Đối tượng | Cơ chế |
|---|---|---|
| Cao | App client (Windows, Android) | Cặp khóa ghép đôi |
| Thường | Trình duyệt | **Mã 6 số tự đổi mỗi phút** (đã làm), sau đó cookie phiên `HttpOnly`. Sau này thêm passkey |
| Thường | App WebDAV bên thứ ba | Khóa truy cập `pnk_…` riêng cho từng app, thu hồi được |

**Chế độ khóa cứng:** chỉ chấp nhận thiết bị đã ghép đôi bằng cặp khóa, tắt mã một lần và WebDAV dùng mật khẩu.

### 4.4 Thu hồi và khôi phục
- Thu hồi từng thiết bị, hoặc thu hồi tất cả.
- Mất client → thu hồi trên server. Mất server → client dùng "Quên server này".
- **[Chưa chốt]** Sao lưu danh tính server (mã hóa bằng mật khẩu) để đổi điện thoại không phải ghép lại.

## 5. Vận hành trên MagicOS / Android
- Hướng dẫn trong app: quản lý khởi chạy thủ công, bỏ tối ưu pin, khóa app trong đa nhiệm, không tự tắt hotspot.
- Watchdog: theo dõi mạng thay đổi, tự nối lại tsnet.
- Cảnh báo pin yếu / nhiệt độ cao. Khuyến nghị giới hạn sạc 80%.
- **[Chưa chốt]** Báo qua Telegram (dùng bot riêng).

## 6. Câu hỏi còn mở
- [ ] Đăng nhập thiết bị mới có cần bấm "Cho phép" trên server không?
- [ ] Chế độ khóa cứng: bật hay tắt mặc định?
- [ ] Passkey cho iPhone có làm ngay ở bản đầu không?
- [ ] Tự backup ảnh có phải tính năng chính của client Android không?
- [ ] Client Windows: gắn ổ đĩa (cần WinFsp) hay chỉ cửa sổ duyệt file?

## 7. Thiết bị server thực tế (đo qua ADB, 2026-10-01)

| Thông số | Giá trị |
|---|---|
| Máy | HONOR ABR-NX1 (Honor 400 Lite) |
| Hệ điều hành | Android 16 (SDK 36), MagicOS 10.0.0, bản vá 2026-09-01 |
| CPU ABI | arm64-v8a |
| Bộ nhớ | 223 GB, còn trống 124 GB |
| Keystore | Có Keystore phần cứng (TEE), **không có StrongBox** |
| USB OTG | Có (`usb.host`) |
| Bootloader | Khóa (không root) |
| Mạng lúc đo | wlan0 `192.168.100.14/24` |

APK server khung đã cài và chạy được trên máy.

## 8. Đăng nhập trình duyệt (đã chốt và đã làm, 2026-10-01)

- **Mã:** 6 chữ số, tự đổi vào đầu mỗi phút. App hiện mã liên tục, có thanh đếm ngược, không có nút tạo mã. Mở app là server tự chạy.
- Mỗi mã dùng 1 lần. Mã cũ còn được chấp nhận thêm 10 giây sau khi đổi. Sai 5 lần thì đổi mã sớm.
- **Giới hạn thử sai:** mỗi IP 10 lần / 15 phút; toàn hệ thống 20 lần / 5 phút, quá mức thì khóa đăng nhập 5 phút.
- **Phiên:** cookie phiên (không có Max-Age), tắt trình duyệt là mất. Server cắt phiên tối đa 24 giờ sau khi đăng nhập, không gia hạn.
- Thiết bị đã đăng nhập hiện trong app, thu hồi được. Có thông báo khi có thiết bị mới đăng nhập, hoặc khi mã bị đổi sớm do nhập sai nhiều.
- Mật khẩu chỉ còn dùng cho WebDAV. Sẽ thay bằng khóa truy cập riêng cho từng app.

## 9. Ghi chú mạng thực tế

- Mạng nhà người dùng có thể có **hai lớp NAT** (router phụ). Khi đó máy tính không truy cập được server qua LAN, nhưng tailnet vẫn kết nối trực tiếp P2P được.
- Đây là một lý do để bỏ chế độ LAN: tailnet đi xuyên được double NAT.
- Logcat trên MagicOS bị mã hóa. App tự ghi `pnas.log` và `go-crash.txt` vào `/sdcard/Android/data/com.pocketnas.server/files/`.

## 10. Funnel trên Android (đã chạy, 2026-10-01)

- Chuẩn bị tailnet một lần: bật **HTTPS Certificates** ở trang admin/dns, rồi thêm `nodeAttrs` `funnel` cho `autogroup:member` trong file ACL.
- **Lỗi của tsnet trên Android:** `tailscale.com/ipn/localapi/cert.go` có build tag `!android`, nên tsnet không xin được chứng chỉ (LocalAPI trả 404, bắt tay TLS lỗi `SSL_ERROR_INTERNAL_ERROR_ALERT`). Cách sửa: `core/mobile/cert_android.go` tự đăng ký lại endpoint `cert/` qua `localapi.Register`, gọi `LocalBackend.GetCertPEMWithValidity`. Phải kiểm tra lại mỗi khi nâng cấp tailscale.com.
- App xin chứng chỉ ngay khi Funnel mở (lần đầu khoảng 40 giây), và ghi lỗi bắt tay TLS vào nhật ký với tần suất có giới hạn.
- Truy cập công khai đi qua relay Funnel của Tailscale (IP 103.84.155.x); trong tailnet thì đi thẳng.

## 11. Mất mạng / đổi mạng (đã làm, 2026-10-01)

- Server **không dừng khi mất mạng**. Thông báo hiện "⚠ Mất mạng, đang chờ kết nối lại".
- `NasService` theo dõi mạng mặc định bằng `ConnectivityManager.registerDefaultNetworkCallback`. Khi có mạng hoặc đổi mạng, sau 1,5 giây (gom các sự kiện dồn dập) sẽ gọi `Mobile.NetworkChanged()`, hàm này chạy LocalAPI `rebind` và `restun`, nên Tailscale tìm đường mới ngay lập tức.
- Đã kiểm tra trên máy thật: tắt rồi bật lại Wi-Fi, Funnel truy cập lại được nhanh, địa chỉ và phiên đăng nhập trình duyệt giữ nguyên.
- Thông báo cố định: `Tailnet ✓ (Wi-Fi) • Funnel • N kết nối • ↓x ↑y • N thiết bị`. Tốc độ tính từ bộ đếm byte trên mọi listener (`core/mobile/traffic.go`), cập nhật mỗi 2 giây.

## 12. Chạy nền trên MagicOS (đã kiểm tra, 2026-10-01)

- Bấm "Đóng mọi ứng dụng gần đây" hoặc vuốt xóa thẻ app: foreground service vẫn chạy, PID không đổi, Funnel vẫn trả lời.
- Server chỉ dừng khi: bấm Dừng (trên thông báo hoặc trong app), Buộc dừng trong Cài đặt, hoặc MagicOS dọn app nếu chưa chỉnh "Khởi chạy ứng dụng".
- Hướng dẫn người dùng: Pin → Khởi chạy ứng dụng → quản lý thủ công (bật cả 3 mục); khóa thẻ trong đa nhiệm; bật "Tự chạy khi khởi động máy". Cần đưa phần này thành màn hình hướng dẫn trong app (theo từng hãng) trước khi phát hành.
