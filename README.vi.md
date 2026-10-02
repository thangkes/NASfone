# NASfone

[English](README.md) · **Tiếng Việt**

Biến một chiếc điện thoại Android cũ (hoặc một máy tính Windows) thành NAS, truy cập được từ bất cứ đâu.

NASfone có **một app cho mỗi nền tảng**. Lần đầu mở app, bạn chọn máy đó dùng để làm gì:

| | 📦 **Server**: chứa file | 📲💻 **Client**: dùng NAS ở máy khác |
|---|---|---|
| **Android** | Chia sẻ bộ nhớ điện thoại | Duyệt, tải lên/tải về, NAS hiện trong app Tệp, sao lưu ảnh |
| **Windows** | Chia sẻ một thư mục của máy | Gắn mỗi NAS đã ghép thành một ổ đĩa (`P:`, `Q:`…) |
| **iPhone / iPad** (beta) | — (iOS không cho chạy server nền) | Duyệt, xem, tải lên/tải về, sao lưu ảnh |

Vai trò đổi được sau này trong phần Cài đặt. Server tham gia mạng
[Tailscale](https://tailscale.com) của chính bạn (nhúng sẵn, không cần cài app Tailscale riêng).
Trình duyệt bất kỳ cũng dùng làm client được.

> Trạng thái: giai đoạn đầu (0.x). Đang dùng hằng ngày trên máy của tác giả, có thể còn lỗi vặt.
> **Tải về:** [bản phát hành mới nhất](https://github.com/thangkes/NASfone/releases/latest):
> `NASfone-Android-<phiên bản>.apk`, `NASfone-Windows-Setup-<phiên bản>.exe` và `NASfone-iOS-<phiên bản>.ipa` (beta, xem bên dưới).

## Tính năng

**Vai trò server**

- **Chạy nền:**
  - Android: dịch vụ nền, không bị tắt khi "xoá tất cả" app gần đây, vẫn chạy khi mất mạng và kết nối lại ngay khi có mạng.
  - Windows: chạy ở khay hệ thống và tự khởi động cùng Windows.
- **Dùng tài khoản Tailscale của bạn:** đăng nhập, đăng xuất, đổi tài khoản ngay trong app. Hỗ trợ cả [Headscale](https://github.com/juanfont/headscale). Không có gì gán cứng.
- **Link HTTPS công khai qua Tailscale Funnel** (tuỳ chọn).
- **Trình duyệt file trên web và WebDAV:** khi tải lên file trùng tên thì hỏi ghi đè, giữ cả hai hay bỏ qua.
- **Không dùng mật khẩu.** Trình duyệt đăng nhập bằng mã 6 số xoay vòng:
  - mã **Admin** (toàn quyền) và mã **User** (chỉ xem và tải về), không bao giờ trùng nhau;
  - phiên đăng nhập mất khi tắt trình duyệt (tối đa 24 giờ).
- **App đã ghép đôi:** mỗi thiết bị client có khoá riêng và một quyền (Admin hoặc User). Đổi quyền hay thu hồi lúc nào cũng được.
- **Kết nối LAN không cần internet** (tuỳ chọn). Trình duyệt cùng mạng mở `http://<IP server>:8080`, đăng nhập bằng mã 6 số hoặc **mã QR**:
  - server là điện thoại thì điện thoại quét QR trên trang;
  - server là Windows thì bạn bấm cho phép trong cửa sổ server, sau khi đối chiếu mã 4 ký tự;
  - phiên QR chỉ xem, không lưu lại, tự kết thúc sau 1 giờ không dùng hoặc khi IP của server đổi;
  - kết nối LAN là HTTP thường, chỉ nên dùng ở mạng tin cậy.
- **Tự sao lưu cấu hình** trên Android (`Download/NASfone-config-backup.zip`). Cài lại app là được hỏi khôi phục.

**Vai trò client**

- **Ghép đôi:** quét mã QR của server, bấm "Kết nối app trên máy này" trên trang web của server, hoặc dán lời mời. App luôn hỏi xác nhận trước khi ghép.
- **Tự chọn đường nhanh nhất:** LAN, rồi tailnet, rồi Funnel. Mỗi lần đăng nhập đều kiểm tra danh tính server.
- **Android:**
  - khoá nằm trong Android Keystore;
  - duyệt, mở, chia sẻ, tải về; tải lên với quyền Admin;
  - NAS hiện trong **app Tệp** và trong mọi bộ chọn file;
  - **tự sao lưu ảnh và video**, có tuỳ chọn chỉ khi có Wi-Fi.
- **Kết nối nhiều server cùng lúc** (Android, iPhone và Windows): ví dụ ghép cả điện thoại lẫn PC server rồi chuyển qua lại.
- **Windows:**
  - mỗi server là một ổ đĩa riêng (`P:`, `Q:`…) với dung lượng trống thật; đổi được ký tự ổ cho từng server;
  - quyền User thì ổ chỉ đọc; server nào thu hồi máy này thì chỉ ổ đó bị ngắt, các ổ khác vẫn chạy.

**Chung:** tự cập nhật từ GitHub bằng một chạm (kiểm tra SHA-256, giữ nguyên cấu hình). Giao diện tiếng Anh và tiếng Việt ở mọi nơi.

## Cách hoạt động

```
                ┌──────────────────── core (Go) ─────────────────────┐
                │ mã · ghép đôi · WebDAV · web UI · LAN · cập nhật    │
                │ Tailscale nhúng (tsnet) · Funnel · lõi client       │
                └──────────────┬───────────────────────┬─────────────┘
                      gomobile │                        │ go build
                 ┌─────────────▼──────────┐   ┌─────────▼──────────────┐
                 │ android/  (một APK)    │   │ windows/  (một EXE)    │
                 │ vai trò server · client│   │ vai trò server · client│
                 └────────────────────────┘   └────────────────────────┘
```

- [docs/DESIGN.md](docs/DESIGN.md): kiến trúc và các quyết định thiết kế
- [docs/PAIRING.md](docs/PAIRING.md): giao thức ghép đôi và xác thực

## Bắt đầu

1. **Server:**
   1. Cài NASfone lên máy dùng để chứa file, chọn **Làm server**.
   2. Cấp các quyền app yêu cầu.
   3. Trong mục **Tài khoản Tailscale**, bấm **Đăng nhập** bằng tài khoản của bạn.
   4. Tuỳ chọn: bật Funnel (link công khai) hoặc kết nối LAN.
2. **Trình duyệt:** mở địa chỉ server hiện ra, đăng nhập bằng mã Admin hoặc User.
3. **Client:** cài NASfone lên máy kia, chọn **Làm client**, rồi ghép đôi theo một trong hai cách:
   - quét mã QR ở mục "Ghép thiết bị mới" trên server;
   - trên Windows, bấm **Kết nối app trên máy này** ở trang web của server.

### iPhone / iPad (beta)

File `NASfone-iOS-<phiên bản>.ipa` được build tự động bằng GitHub Actions và **chưa được ký**
(chưa có trên App Store hay TestFlight). Cài bằng [Sideloadly](https://sideloadly.io) hoặc
[AltStore](https://altstore.io) với Apple ID của bạn. Apple ID miễn phí thì cần ký lại app
mỗi 7 ngày. Sau đó ghép đôi như client Android: quét mã QR của server. File tải về nằm trong
app Tệp, mục *Trên iPhone › NASfone*.

> Một số hãng điện thoại tắt app nền rất mạnh tay. Hãy cho phép NASfone chạy nền / tự
> khởi chạy trong phần cài đặt pin của máy.

## Build từ mã nguồn

**Cần:**
- Go 1.27+, [gomobile](https://pkg.go.dev/golang.org/x/mobile/cmd/gomobile), Android SDK + NDK, JDK đi kèm Android Studio.
- Riêng bộ cài Windows cần thêm:
  - [go-winres](https://github.com/tc-hib/go-winres);
  - [Inno Setup 6](https://jrsoftware.org/isinfo.php);
  - [rclone](https://rclone.org) (`winget install Rclone.Rclone`);
  - file MSI có chữ ký của [WinFsp](https://github.com/winfsp/winfsp/releases), đặt trong `windows/installer/deps/`.

```powershell
.\scripts\build-android.ps1                                  # lõi Go -> AAR -> APK
.\scripts\build-android.ps1 -Install -Device <adb serial>    # ...và cài lên máy
.\scripts\build-android.ps1 -Emulator -Install -Device emulator-5554   # kèm x86_64 cho máy ảo
.\scripts\build-windows.ps1                                  # NASfone.exe + windows\dist\NASfone-Windows-Setup-<phiên bản>.exe
.\scripts\release.ps1 0.2.1 -NotesFile notes.md              # tăng phiên bản, build cả hai, tag v0.2.1, phát hành
```

Để sửa giao diện web nhanh có server chạy local: `cd core; go run ./cmd/devserver`
(thêm `-lan` để thử trang đăng nhập bằng QR).

> APK release được ký bằng khoá trong `~/.nasfone-signing/keystore.properties` (nằm ngoài
> repo). Không có khoá này thì build dùng debug key. Android chỉ nhận bản cập nhật ký cùng
> khoá, nên hãy sao lưu khoá.

## Cấu trúc thư mục

| Thư mục | Nội dung |
|---|---|
| [`core/`](core/) | Mã Go dùng chung: server (`server`, `mobile`), client (`client`, `mobileclient`), ghép đôi, xác thực, cập nhật |
| [`android/`](android/) | App Android: `com.nasfone.server` (vai trò server, màn hình chọn vai trò) và `com.nasfone.client` (vai trò client) |
| [`windows/`](windows/) | App Windows: vai trò client (gắn ổ) và vai trò server (`srv/`), bộ cài |
| [`ios/`](ios/) | Client iOS (SwiftUI + lõi Go client); build bằng [`.github/workflows/ios.yml`](.github/workflows/ios.yml) |
| [`scripts/`](scripts/) | Script build và phát hành |
| [`docs/`](docs/) | Ghi chú thiết kế |

## Lộ trình

- Khoá truy cập WebDAV cho app bên thứ ba
- Bộ cài Windows có chữ ký số
- iOS: TestFlight / App Store, NAS hiện trong app Tệp

## Giấy phép

[MIT](LICENSE). Bộ cài Windows kèm rclone (MIT) và WinFsp (GPLv3 kèm ngoại lệ FLOSS);
xem [THIRD-PARTY-NOTICES](windows/installer/THIRD-PARTY-NOTICES.txt).
NASfone không liên kết với Tailscale Inc.
