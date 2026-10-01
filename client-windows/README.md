# client-windows (EXE)

App Windows kết nối tới PocketNAS Server.

- Ghép đôi: dán chuỗi ghép đôi hoặc nhập mã một lần. Khóa danh tính giữ trong TPM, nếu không có TPM thì dùng DPAPI.
- Gắn PocketNAS thành ổ `P:\` qua WinFsp.
- Biểu tượng khay hệ thống: trạng thái LAN / Internet / mất kết nối, mở ổ, ngắt kết nối.
- Chạy cùng Windows, tự nối lại khi mất kết nối.

Công nghệ dự kiến: Go + Wails, dùng chung `../core`. Output là một file `PocketNAS.exe`.

**Chưa khởi tạo:** cần cài Go và Wails CLI.
