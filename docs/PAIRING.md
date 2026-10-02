# NASfone – Đăng nhập, phân quyền và ghép đôi

## 0. Đã chốt (2026-10-01)

| Cơ chế | Dùng cho | Phân quyền |
|---|---|---|
| **SSO – mã 6 số** | Đăng nhập một lần trên trình duyệt | **Hai mã riêng: Admin và User**, đổi mỗi phút |
| **Private key – ghép đôi** | Kết nối liên tục từ app Windows (EXE) / app Android (APK) | Chọn Admin hoặc User khi tạo lời mời |

- **Admin:** toàn quyền với NAS (xem, tải về, tải lên, ghi đè, xóa, tạo thư mục, đổi tên).
- **User:** chỉ **xem và tải về**. Server chặn mọi thao tác ghi bằng mã 403: PUT, DELETE, MKCOL, MOVE, COPY, PROPPATCH, LOCK, POST, kiểm tra trùng tên, upload đo tốc độ.
- **Hai mã SSO không bao giờ trùng nhau tại bất kỳ thời điểm nào**, kể cả mã đang trong 10 giây ân hạn. Mỗi lần sinh mã (đổi theo phút, đổi sau khi dùng, đổi sớm do nhập sai), mã mới phải khác mọi mã còn hiệu lực của cả hai quyền. Một mã nhập vào vì vậy chỉ khớp với tối đa một quyền; nếu khớp từ hai quyền trở lên (không thể xảy ra) thì từ chối. Đã kiểm chứng bằng test 3.000 bước với bộ sinh mã bị ép trùng, kèm test đột biến xác nhận test bắt được lỗi.
- Dùng mã Admin thì chỉ mã Admin đổi, mã User giữ nguyên, và ngược lại.
- Phiên tạo từ trước khi có phân quyền được hạ về **User**.


## 1. Mục tiêu

- Mỗi thiết bị client (app Windows, app Android) có **cặp khóa riêng**. Private key không bao giờ rời khỏi máy.
- Server và client **ghi nhớ public key của nhau**. Client chỉ nói chuyện với đúng server đó, server chỉ phục vụ đúng các client đã ghép.
- Thu hồi từng thiết bị ngay lập tức, không ảnh hưởng thiết bị khác.
- Hoạt động giống nhau qua tailnet và qua Funnel.

## 2. Thuật toán khóa

**ECDSA P-256** cho mọi bên. Không dùng Ed25519 vì TPM trên Windows và Android Keystore (TEE) hỗ trợ P-256 rộng rãi nhất, nên private key có thể nằm trong phần cứng.

| Bên | Nơi giữ private key |
|---|---|
| Server (điện thoại) | Lõi Go giữ khóa trong RAM khi chạy. Trên đĩa, khóa được mã hóa bằng AES-GCM với khóa nằm trong Android Keystore (TEE), không xuất ra được |
| Client Android | Android Keystore (TEE/StrongBox), không xuất ra được |
| Client Windows | TPM qua CNG (Microsoft Platform Crypto Provider). Máy không có TPM thì dùng khóa phần mềm mã hóa bằng DPAPI |

**Vân tay** = SHA-256 của public key (định dạng SPKI), hiển thị dạng rút gọn `ABCD-EFGH-…` để người dùng đối chiếu nếu cần.

## 3. Luồng ghép đôi

```
 Điện thoại (server)                                    Client (Windows/Android)
 1. Bấm "Ghép thiết bị mới", chọn Admin / User
    → tạo LỜI MỜI dùng 1 lần, hạn 5 phút:
      nasfone1:<base64url {v, url, fp_server, token}>
      hiện dạng QR + nút Sao chép
                       ──── QR / copy-paste ────►
                                                        2. Tạo cặp khóa trong phần cứng
                                                        3. POST /__nasfone/pair
                                                           {token, client_pub, name, platform, nonce_c}
 4. Kiểm tra token (1 lần, còn hạn)
    lưu thiết bị {id, name, client_pub, quyền}
    trả {device_id, server_pub,
         sig_server("nasfone-pair-v1" | token | client_pub | device_id | nonce_c)}
                       ◄──────────────────────────
                                                        5. Kiểm tra SHA-256(server_pub) == fp_server
                                                           và chữ ký → lưu server_pub (pin)
 6. Thông báo "Đã ghép: <tên>"
```

- Lời mời chứa vân tay server, nên client phát hiện được server giả **ngay từ lần đầu**.
- Token chỉ dùng 1 lần và hết hạn sau 5 phút. Lộ lời mời cũ cũng vô hại.
- Lời mời do chủ máy tạo trực tiếp trên điện thoại server, nên bản thân nó đã là sự cho phép. Không cần bấm duyệt thêm.

## 4. Xác thực mỗi lần kết nối (hai chiều)

```
Client                                                   Server
POST /__nasfone/auth/challenge {device_id, nonce_c}  ──►
                                                   ◄──   {nonce_s, sig_server("nasfone-auth-s" | nonce_c | nonce_s)}
kiểm tra chữ ký server bằng server_pub đã pin
POST /__nasfone/auth {device_id, nonce_s,
     sig_client("nasfone-auth-c" | nonce_s | nonce_c | host)} ──►
                                                   ◄──   {access_token, expires_in: 3600}
```

- Server chứng minh danh tính trước, nên client không bao giờ gửi gì cho server giả.
- **Access token** sống 1 giờ và chỉ lưu trong RAM của client. Hết hạn thì client tự xác thực lại.
- Mọi yêu cầu sau đó, kể cả WebDAV, đều gửi kèm `Authorization: Bearer <token>`. App Windows dùng rclone (WebDAV + `bearer_token_command`) nên tương thích sẵn.
- Thu hồi thiết bị thì toàn bộ token của thiết bị đó mất hiệu lực ngay.

## 5. Quản lý trên app server

- Danh sách **"Thiết bị đã ghép"**: tên, nền tảng, vân tay rút gọn, lần cuối kết nối, quyền. Có nút Thu hồi.
- Tách riêng với danh sách **phiên trình duyệt** (đăng nhập bằng mã 6 số).
- Chế độ **khóa cứng** (tùy chọn): tắt đăng nhập bằng mã 6 số, chỉ thiết bị đã ghép mới vào được.

## 6. Endpoint mới

| Endpoint | Cần xác thực | Mục đích |
|---|---|---|
| `POST /__nasfone/pair` | Token lời mời | Ghép đôi |
| `POST /__nasfone/auth/challenge` | Không | Bắt đầu xác thực |
| `POST /__nasfone/auth` | Chữ ký | Lấy access token |
| Các endpoint hiện có | Cookie trình duyệt **hoặc** Bearer token | Như cũ |

Giới hạn tần suất áp dụng giống đăng nhập bằng mã: theo IP và toàn hệ thống.

## 7. Câu hỏi cần chốt

1. ~~Cách ghép~~ → đã chốt: SSO chỉ dùng cho web; ghép đôi app bằng lời mời QR / chuỗi copy.
2. ~~Quyền khi ghép~~ → đã chốt: chọn **Admin / User** khi tạo lời mời trên server.
3. Có cần **khóa truy cập WebDAV** cho app bên thứ ba (RaiDrive, CX File Explorer…) không? Mỗi app một khóa riêng, thu hồi được. Đây không phải mật khẩu chung như trước.
4. Có cần **chế độ khóa cứng** không? Bật hay tắt mặc định?
