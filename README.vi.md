# NASfone

[English](README.md) · **Tiếng Việt**

Biến một chiếc điện thoại Android cũ thành NAS bỏ túi, truy cập được từ bất cứ đâu.

NASfone chạy một server file nhỏ trên điện thoại và đưa nó vào mạng
[Tailscale](https://tailscale.com) của chính bạn (nhúng sẵn, không cần cài app Tailscale riêng).
Mở bằng bất kỳ trình duyệt nào, hoặc gắn thành ổ đĩa trên Windows.

> Trạng thái: giai đoạn đầu (0.1). Đang dùng hằng ngày trên điện thoại của tác giả, có thể còn lỗi vặt.

## Tính năng

- **App server cho Android** — chạy dạng dịch vụ nền, không bị tắt khi "xoá tất cả" ứng
  dụng gần đây, vẫn chạy khi mất mạng và kết nối lại ngay khi có Wi-Fi hoặc 3G/4G.
  Thanh thông báo hiện trạng thái và lưu lượng trực tiếp.
- **Dùng tài khoản Tailscale của bạn** — đăng nhập, đăng xuất, đổi tài khoản ngay trong
  app. Hỗ trợ control server riêng (ví dụ [Headscale](https://github.com/juanfont/headscale)).
  Không có gì gán cứng.
- **Link HTTPS công khai qua Tailscale Funnel** (tuỳ chọn) — mở file từ trình duyệt
  không nằm trong tailnet.
- **Trình duyệt file trên web** — tải lên, tải xuống, tạo thư mục, xoá; khi tải lên file
  trùng sẽ hỏi ghi đè, giữ cả hai hay bỏ qua. Có cả endpoint WebDAV chuẩn.
- **Không dùng mật khẩu** — đăng nhập web bằng mã 6 số xoay vòng hiển thị trong app:
  - mã **Admin** (toàn quyền) và mã **User** (chỉ xem và tải về), không bao giờ trùng nhau;
  - phiên đăng nhập mất khi tắt trình duyệt (tối đa 24 giờ phía server).
- **Thiết bị ghép đôi có khoá riêng** — mỗi app client có cặp khoá riêng và một quyền
  (Admin hoặc User). Thu hồi hay đổi quyền ngay trên điện thoại.
- **App Windows** — biểu tượng khay hệ thống, gắn điện thoại thành ổ đĩa (mặc định `P:`)
  với dung lượng trống thật, chỉ đọc với quyền User, tự nhận biết khi bị thu hồi. Ghép
  đôi một chạm từ trang web ("Kết nối app trên máy này").
- **Tự cập nhật từ GitHub Releases** — cả hai app tự tìm bản mới và cập nhật bằng một chạm
  (kiểm tra SHA-256; Android sẽ hỏi xác nhận khi cài).
- **Tiếng Anh và tiếng Việt** ở mọi nơi (app điện thoại, web, app Windows).

## Cách hoạt động

```
                 ┌──────────────── core (Go) ─────────────────┐
                 │ mã đăng nhập · ghép đôi · WebDAV · web UI  │
                 │ Tailscale nhúng (tsnet) · Funnel           │
                 └───────┬────────────────┬──────────────┬────┘
                gomobile │       gomobile │     go build │
          ┌──────────────▼───┐  ┌─────────▼────────┐  ┌──▼───────────────┐
          │ server-android   │  │ client-android   │  │ client-windows   │
          │ (APK, làm NAS)   │  │ (dự kiến)        │  │ (EXE + bộ cài)   │
          └──────────────────┘  └──────────────────┘  └──────────────────┘
```

- [docs/DESIGN.md](docs/DESIGN.md) — kiến trúc và các quyết định thiết kế
- [docs/PAIRING.md](docs/PAIRING.md) — giao thức ghép đôi và xác thực

## Bắt đầu

1. Cài **APK server** lên điện thoại dùng để chứa file và mở app.
2. Cấp các quyền app yêu cầu (truy cập mọi file, thông báo, bỏ tối ưu pin).
3. Trong mục **Tài khoản Tailscale**, bấm **Đăng nhập** và đăng nhập bằng tài khoản của bạn.
4. Mở địa chỉ hiện trong app từ thiết bị cùng tailnet (hoặc bật Funnel để có link HTTPS
   công khai) rồi đăng nhập bằng mã Admin hoặc User đang hiện.
5. Trên Windows: cài `NASfone-Setup-<phiên bản>.exe`, rồi trên trang web bấm
   **Kết nối app trên máy này** và xác nhận trong hộp thoại NASfone.

> Một số hãng điện thoại tắt app nền rất mạnh tay. Hãy cho phép NASfone chạy nền / tự
> khởi chạy trong phần cài đặt pin của máy.

## Build từ mã nguồn

Cần: Go 1.27+, [gomobile](https://pkg.go.dev/golang.org/x/mobile/cmd/gomobile),
Android SDK + NDK, JDK đi kèm Android Studio. Riêng bộ cài Windows cần thêm
[go-winres](https://github.com/tc-hib/go-winres), [Inno Setup 6](https://jrsoftware.org/isinfo.php),
[rclone](https://rclone.org) (`winget install Rclone.Rclone`) và file MSI có chữ ký của
[WinFsp](https://github.com/winfsp/winfsp/releases) đặt trong `client-windows/installer/deps/`.

```powershell
.\scripts\build-server.ps1             # lõi Go -> AAR -> APK server
.\scripts\build-server.ps1 -Install    # ...và cài lên điện thoại qua adb
.\scripts\build-windows.ps1            # NASfone.exe + client-windows\dist\NASfone-Setup-<phiên bản>.exe
.\scripts\release.ps1 0.2.0            # tăng phiên bản, build cả hai, gắn tag và tạo GitHub Release
```

Để sửa giao diện web nhanh có server chạy local: `cd core; go run ./cmd/devserver`.

> APK release được ký bằng khoá trong `~/.nasfone-signing/keystore.properties` (nằm ngoài
> repo). Không có khoá này thì build dùng debug key. Android chỉ nhận bản cập nhật ký cùng
> khoá, nên hãy sao lưu khoá.

## Cấu trúc thư mục

| Thư mục | Nội dung |
|---|---|
| [`core/`](core/) | Mã Go dùng chung: mã đăng nhập, ghép đôi, WebDAV + web UI, gomobile |
| [`server-android/`](server-android/) | App server Android (vỏ Kotlin bọc lõi Go) |
| [`client-windows/`](client-windows/) | App khay Windows, gắn ổ đĩa, bộ cài |
| [`client-android/`](client-android/) | App client Android (dự kiến) |
| [`scripts/`](scripts/) | Script build |
| [`docs/`](docs/) | Ghi chú thiết kế |

## Lộ trình

- App client Android (ghép đôi bằng QR, duyệt file, sao lưu ảnh)
- Khoá truy cập WebDAV cho app bên thứ ba
- Bản phát hành có chữ ký trên GitHub

## Giấy phép

[MIT](LICENSE). Bộ cài Windows kèm rclone (MIT) và WinFsp (GPLv3 kèm ngoại lệ FLOSS);
xem [THIRD-PARTY-NOTICES](client-windows/installer/THIRD-PARTY-NOTICES.txt).
NASfone không liên kết với Tailscale Inc.
