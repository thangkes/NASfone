# client-windows — NASfone.exe

App Windows chạy ở khay hệ thống: ghép đôi với NASfone Server bằng cặp khóa, rồi gắn NAS thành một ổ đĩa (mặc định `P:`).

## Yêu cầu
- **WinFsp:** `winget install WinFsp.WinFsp`. Lúc cài, Windows sẽ hỏi quyền Admin.
- **rclone:** `winget install Rclone.Rclone`, hoặc đặt `rclone.exe` cạnh `NASfone.exe`.

## Build
```powershell
cd client-windows
go build -trimpath -ldflags="-s -w -H=windowsgui" -o build\NASfone.exe .
```

## Cách dùng
1. Chạy `NASfone.exe` một lần. App đăng ký link `nasfone://` và hiện biểu tượng ở khay hệ thống.
2. Trên máy tính, mở trang web NASfone và đăng nhập bằng **mã ADMIN**. Bấm **🔗 Kết nối app trên máy này**, rồi chọn quyền Admin hoặc User.
3. Trình duyệt mở app, app **hỏi xác nhận** (hiện server, quyền và vân tay server). Bấm **Yes**.
4. Sau vài giây, ổ **NASfone** xuất hiện trong File Explorer.

**Dự phòng:** sao chép lời mời `nasfone1:…`, rồi trong menu khay chọn **"Ghép đôi bằng lời mời đã sao chép"**.

## Cách hoạt động
| Phần | Chi tiết |
|---|---|
| Khóa riêng | ECDSA P-256, lưu tại `%APPDATA%\NASfone\key.bin`, mã hóa bằng **DPAPI** (chỉ tài khoản Windows này giải mã được). Dự kiến chuyển sang TPM |
| Cấu hình | `%APPDATA%\NASfone\config.json`: URL, khóa công khai của server (đã pin), device ID, quyền. Không chứa bí mật |
| Ổ đĩa | `rclone mount :webdav: P:` với `--webdav-bearer-token-command="NASfone.exe token"`. Quyền User thì gắn `--read-only` |
| Token | `NASfone.exe token` chạy xác thực hai chiều và in ra token dùng 1 giờ. Rclone gọi lệnh này khi cần |
| Ghi file | Dùng bộ nhớ đệm của rclone (`vfs-cache-mode=full`), file được đẩy lên server khoảng 5 giây sau khi ghi |
| Log | `%APPDATA%\NASfone\rclone.log` |

## Bảo mật
- **Luôn hỏi xác nhận trước khi ghép đôi**, vì bất kỳ trang web nào cũng có thể mở link `nasfone://`.
- Client kiểm tra server có giữ đúng khóa ghi trong lời mời không, ngay khi ghép và ở mỗi lần đăng nhập.

## Test thủ công với devserver
```bash
go run ./core/cmd/devserver -invite admin       # in ra lời mời nasfone1:…
NASFONE_DEV_INVITE=nasfone1:… go test -run TestDevPair ./client-windows/   # ghép đôi, bỏ qua hộp xác nhận
```
