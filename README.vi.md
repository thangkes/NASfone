# NASfone

[English](README.md) · **Tiếng Việt**

Biến một chiếc điện thoại Android cũ thành NAS bỏ túi, truy cập được từ bất cứ đâu.

NASfone chạy một server file nhỏ trên điện thoại và đưa nó vào mạng
[Tailscale](https://tailscale.com) của chính bạn (nhúng sẵn, không cần cài app Tailscale riêng).
Mở bằng bất kỳ trình duyệt nào, hoặc gắn thành ổ đĩa trên Windows.

> Trạng thái: giai đoạn đầu (0.1.x). Đang dùng hằng ngày trên điện thoại của tác giả, có thể còn lỗi vặt.
> **Tải về:** [bản phát hành mới nhất](https://github.com/thangkes/NASfone/releases/latest)
> (APK server cho điện thoại, bộ cài client cho Windows). **Server cho Windows** có bản phát
> hành riêng, tag `windows-server-v…`, trên [trang Releases](https://github.com/thangkes/NASfone/releases) (beta).

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
- **Server cho Windows (NASfone for Windows - Server, beta)** — chia sẻ một thư mục của PC
  theo cùng cách: Tailscale nhúng, Funnel, mã đăng nhập, app đã ghép, kết nối LAN (yêu cầu
  đăng nhập QR qua LAN được duyệt ngay trong cửa sổ), biểu tượng khay, tự khởi động.
- **App client Android (NASfone Client, beta)** — dùng server từ điện thoại khác: ghép đôi
  bằng QR với khoá nằm trong Android Keystore, duyệt / tải lên / tải về, NAS hiện trong app
  Tệp, tự sao lưu ảnh & video, tự chọn đường nhanh nhất (LAN, tailnet, Funnel). Bản phát
  hành có tag `android-client-v…`.
- **App Windows (NASfone for Windows - Client)** — biểu tượng khay hệ thống, gắn điện thoại thành ổ đĩa (mặc định `P:`)
  với dung lượng trống thật, chỉ đọc với quyền User, tự nhận biết khi bị thu hồi. Ghép
  đôi một chạm từ trang web ("Kết nối app trên máy này").
- **Kết nối LAN, không cần internet** (nút gạt tuỳ chọn) — trình duyệt cùng Wi-Fi hoặc
  hotspot của điện thoại mở `http://<IP điện thoại>:8080`, đăng nhập bằng mã 6 số hoặc
  **quét mã QR** trên trang bằng app điện thoại. Phiên QR chỉ xem, không lưu lại, tự kết
  thúc sau 1 giờ không dùng hoặc khi IP LAN của điện thoại đổi. (HTTP thường: chỉ dùng ở
  mạng tin cậy.)
- **Tự sao lưu cấu hình** — app điện thoại luôn cập nhật `Download/NASfone-config-backup.zip`
  (đăng nhập Tailscale, thiết bị đã ghép, cài đặt) và hỏi khôi phục khi cài lại app.
- **Tự cập nhật từ GitHub Releases** — cả hai app tự tìm bản mới và cập nhật bằng một chạm
  (kiểm tra SHA-256; Android sẽ hỏi xác nhận khi cài). Cập nhật giữ nguyên cấu hình.
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
          │ (APK, làm NAS)   │  │ (APK, beta)      │  │ (EXE + bộ cài)   │
          └──────────────────┘  └──────────────────┘  └──────────────────┘
```

- [docs/DESIGN.md](docs/DESIGN.md) — kiến trúc và các quyết định thiết kế
- [docs/PAIRING.md](docs/PAIRING.md) — giao thức ghép đôi và xác thực

## Bắt đầu

1. Tải `NASfone-Server-<phiên bản>.apk` từ
   [bản phát hành mới nhất](https://github.com/thangkes/NASfone/releases/latest), cài lên
   điện thoại dùng để chứa file và mở app.
2. Cấp các quyền app yêu cầu (truy cập mọi file, thông báo, bỏ tối ưu pin).
3. Trong mục **Tài khoản Tailscale**, bấm **Đăng nhập** và đăng nhập bằng tài khoản của bạn.
4. Mở địa chỉ hiện trong app từ thiết bị cùng tailnet (hoặc bật Funnel để có link HTTPS
   công khai) rồi đăng nhập bằng mã Admin hoặc User đang hiện.
5. Trên Windows: cài `NASfone-Windows-Client-Setup-<phiên bản>.exe`, rồi trên trang web bấm
   **Kết nối app trên máy này** và xác nhận trong hộp thoại NASfone.

6. Tuỳ chọn: trong mục **Kết nối LAN**, gạt bật để vào thẳng điện thoại từ cùng mạng, kể cả
   khi không có internet.

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
.\scripts\build-windows.ps1            # NASfone.exe + client-windows\dist\NASfone-Windows-Client-Setup-<phiên bản>.exe
.\scripts\release.ps1 0.2.0            # tăng phiên bản, build cả hai, gắn tag và tạo GitHub Release
.\scripts\build-windows-server.ps1     # NASfoneServer.exe + server-windows\dist\NASfone-Windows-Server-Setup-<phiên bản>.exe
.\scripts\release-windows-server.ps1 0.2.0-beta.1 -NotesFile notes.md   # phát hành server Windows
```

Để sửa giao diện web nhanh có server chạy local: `cd core; go run ./cmd/devserver`
(thêm `-lan` để thử trang đăng nhập bằng QR).

> APK release được ký bằng khoá trong `~/.nasfone-signing/keystore.properties` (nằm ngoài
> repo). Không có khoá này thì build dùng debug key. Android chỉ nhận bản cập nhật ký cùng
> khoá, nên hãy sao lưu khoá.

## Cấu trúc thư mục

| Thư mục | Nội dung |
|---|---|
| [`core/`](core/) | Mã Go dùng chung: mã đăng nhập, ghép đôi, đăng nhập LAN bằng QR, WebDAV + web UI, cập nhật, gomobile |
| [`server-android/`](server-android/) | App server Android (vỏ Kotlin bọc lõi Go) |
| [`client-windows/`](client-windows/) | App khay Windows, gắn ổ đĩa, bộ cài |
| [`server-windows/`](server-windows/) | App server cho Windows (beta) và bộ cài |
| [`client-android/`](client-android/) | App client Android (beta): ghép đôi, duyệt file, app Tệp, sao lưu ảnh |
| [`scripts/`](scripts/) | Script build |
| [`docs/`](docs/) | Ghi chú thiết kế |

## Lộ trình

- Khoá truy cập WebDAV cho app bên thứ ba
- Bộ cài Windows có chữ ký số

## Giấy phép

[MIT](LICENSE). Bộ cài Windows kèm rclone (MIT) và WinFsp (GPLv3 kèm ngoại lệ FLOSS);
xem [THIRD-PARTY-NOTICES](client-windows/installer/THIRD-PARTY-NOTICES.txt).
NASfone không liên kết với Tailscale Inc.
