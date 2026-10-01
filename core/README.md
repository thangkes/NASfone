# core (Go)

Lõi dùng chung cho cả ba app:

- `tsnet`: nút Tailscale nhúng trong app (userspace, Funnel).
- `webdav`: dùng `golang.org/x/net/webdav`, kèm giao diện web.
- `pairing` / `auth`: chuỗi ghép đôi, challenge-response, mã một lần, khóa truy cập.

Build cho Android bằng `gomobile bind` (ra file `.aar`), cho Windows dùng `go build` trực tiếp.

**Chưa khởi tạo:** máy dev chưa cài Go. Cần cài Go 1.24+ và `gomobile` (`go install golang.org/x/mobile/cmd/gomobile@latest`, sau đó chạy `gomobile init`).
