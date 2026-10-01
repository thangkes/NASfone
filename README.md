# PocketNAS

Biến một điện thoại Android cũ thành server lưu trữ di động. Truy cập được qua mạng nội bộ, hotspot của chính điện thoại, hoặc qua Internet bằng Tailscale. Mỗi thiết bị client được ghép đôi bằng **cặp khóa riêng**.

## Cấu trúc

| Thư mục | Sản phẩm | Công nghệ | Trạng thái |
|---|---|---|---|
| [`server-android/`](server-android/) | **APK server**, chạy trên điện thoại làm NAS | Kotlin (vỏ Android) + lõi Go (gomobile) | Khung project, build được |
| [`client-android/`](client-android/) | **APK client**, cho điện thoại khác truy cập NAS | Kotlin + lõi Go (gomobile) | Khung project, build được |
| [`client-windows/`](client-windows/) | **EXE client** cho Windows: gắn ổ `P:\`, biểu tượng khay hệ thống | Go + Wails + WinFsp | Chưa khởi tạo (cần cài Go) |
| [`core/`](core/) | Lõi dùng chung: giao thức ghép đôi, xác thực, WebDAV, tsnet | Go | Chưa khởi tạo (cần cài Go) |
| [`docs/`](docs/) | Tài liệu thiết kế, mockup | — | [DESIGN.md](docs/DESIGN.md), [mockup đăng nhập](docs/mockups/login-mockup.html) |

```
                 ┌────────────── core (Go) ──────────────┐
                 │ pairing · auth · webdav · tsnet       │
                 └───────┬───────────────┬───────────────┘
                gomobile │      gomobile │        go build
          ┌──────────────▼───┐  ┌────────▼─────────┐  ┌──────────────────┐
          │ server-android   │  │ client-android   │  │ client-windows   │
          │ (APK)            │  │ (APK)            │  │ (EXE)            │
          └──────────────────┘  └──────────────────┘  └──────────────────┘
```

## Build

Yêu cầu: Android SDK (platform 36), JDK đi kèm Android Studio (`jbr`). Riêng `core` và `client-windows` cần thêm Go 1.24+ và gomobile.

```powershell
$env:JAVA_HOME = "C:\Program Files\Android\Android Studio\jbr"
cd server-android; .\gradlew.bat assembleRelease   # -> app\build\outputs\apk\release\app-release.apk
cd ..\client-android; .\gradlew.bat assembleRelease
```

> Bản release hiện tạm ký bằng debug key để cài thử. Phải tạo keystore riêng trước khi phát hành.

## Lộ trình

1. **Kiểm chứng tsnet trên Honor**: đăng nhập Tailscale trong app, phục vụ web qua tailnet và Funnel, đo tốc độ.
2. **Server hoàn chỉnh**: WebDAV, giao diện web, foreground service, tự khởi động, ghép đôi bằng cặp khóa, danh sách thiết bị tin cậy.
3. **Client Windows (EXE)**: ghép đôi, gắn ổ `P:\`, tự chọn LAN hoặc Funnel, biểu tượng khay hệ thống.
4. **Client Android**: ghép đôi bằng QR, duyệt file, DocumentsProvider, tự backup ảnh.
5. **Phát hành**: GitHub Releases, ký số, đa ngôn ngữ, hướng dẫn chống diệt app nền theo từng hãng.

Chi tiết xem [docs/DESIGN.md](docs/DESIGN.md).
