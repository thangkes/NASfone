import SwiftUI
import UIKit
import Nasfonecore

/// Home: paired servers, pairing, backup and settings.
struct HomeView: View {
    @EnvironmentObject var servers: Servers
    @EnvironmentObject var model: AppModel
    @State private var scanning = false
    @State private var pasting = false
    @State private var pasteText = ""
    @State private var confirm: Invite?
    @State private var message: String?
    @State private var busy = false
    @State private var settings = false

    struct Invite: Identifiable {
        let id = UUID()
        let text: String
        let host: String
        let admin: Bool
        let fp: String
    }

    var body: some View {
        NavigationStack {
            List {
                if let upd = model.update, let v = upd["version"] as? String {
                    Section {
                        Button {
                            if let s = upd["pageURL"] as? String, let u = URL(string: s) { UIApplication.shared.open(u) }
                        } label: {
                            Label(L("Đã có NASfone v\(v) — mở trang tải về", "NASfone v\(v) is available — open the download page"),
                                  systemImage: "arrow.down.circle")
                        }
                    }
                }
                Section {
                    if servers.list.isEmpty {
                        Text(L("Chưa ghép server nào.", "No server paired yet.")).foregroundStyle(.secondary)
                    }
                    ForEach(servers.list) { s in ServerRow(server: s) }
                } header: {
                    Text(L("Máy chủ NASfone đã ghép", "Paired NASfone servers"))
                } footer: {
                    Text(L("Mỗi server có khoá riêng nằm trong Secure Enclave của máy này.",
                           "Each server has its own key kept in this device's Secure Enclave."))
                }
                Section {
                    Button { scanning = true } label: { Label(L("Quét mã ghép đôi", "Scan pairing code"), systemImage: "qrcode.viewfinder") }
                    Button {
                        pasteText = UIPasteboard.general.string.flatMap { MobileclientQRText($0) == "invite" ? $0 : nil } ?? ""
                        pasting = true
                    } label: { Label(L("Dán lời mời", "Paste invite"), systemImage: "doc.on.clipboard") }
                } footer: {
                    Text(L("Lấy mã ghép đôi: trên server (app Android hoặc NASfone trên Windows) bấm \"Ghép thiết bị mới\".",
                           "Get a pairing code: on the server (Android app or NASfone on Windows) tap \"Pair a new device\"."))
                }
            }
            .navigationTitle("NASfone")
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button { settings = true } label: { Image(systemName: "gearshape") }
                }
            }
            .overlay { if busy { ProgressView(L("Đang ghép đôi…", "Pairing…")).padding().background(.regularMaterial).clipShape(RoundedRectangle(cornerRadius: 12)) } }
            .sheet(isPresented: $scanning) {
                ScannerSheet { code in
                    scanning = false
                    if MobileclientQRText(code) == "invite" { ask(code) }
                    else { message = L("Đây không phải mã ghép đôi NASfone. (Mã QR đăng nhập LAN dành cho server.)",
                                       "This is not a NASfone pairing code. (LAN sign-in codes are for the server.)") }
                }
            }
            .sheet(isPresented: $settings) { SettingsView() }
            .alert(L("Dán lời mời ghép đôi", "Paste the pairing invite"), isPresented: $pasting) {
                TextField("nasfone1:…", text: $pasteText)
                Button(L("Tiếp tục", "Continue")) { ask(pasteText) }
                Button(L("Huỷ", "Cancel"), role: .cancel) {}
            }
            .alert(item: $confirm) { inv in
                Alert(title: Text(L("Ghép đôi với server này?", "Pair with this server?")),
                      message: Text("Server: \(inv.host)\n" +
                                    L("Quyền: ", "Access: ") + (inv.admin ? L("ADMIN — toàn quyền", "ADMIN — full access") : L("USER — chỉ xem & tải về", "USER — view & download only")) +
                                    L("\nMã nhận dạng server: ", "\nServer key: ") + inv.fp +
                                    L("\n\nChỉ đồng ý nếu bạn vừa tạo lời mời này trên server của mình.", "\n\nOnly accept if you just created this invite on your own server.")),
                      primaryButton: .default(Text(L("Ghép đôi", "Pair"))) { pair(inv.text) },
                      secondaryButton: .cancel())
            }
            .alert(message ?? "", isPresented: Binding(get: { message != nil }, set: { if !$0 { message = nil } })) {
                Button("OK", role: .cancel) {}
            }
            .onChange(of: model.pendingInvite) { inv in
                if let inv = inv { model.pendingInvite = nil; ask(inv) }
            }
        }
    }

    /// Pairing always asks first: any app or page can hand us an invite.
    private func ask(_ text: String) {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        var err: NSError?
        let js = MobileclientInviteInfo(t, &err)
        guard err == nil, let info = (try? JSONSerialization.jsonObject(with: Data(js.utf8))) as? [String: String] else {
            message = L("Lời mời không hợp lệ: ", "Invalid invite: ") + (err?.localizedDescription ?? "")
            return
        }
        confirm = Invite(text: t, host: info["host"] ?? "", admin: info["role"] == "admin", fp: info["fp"] ?? "")
    }

    private func pair(_ invite: String) {
        busy = true
        DispatchQueue.global().async {
            let tag = "nasfone-" + UUID().uuidString
            var msg: String
            do {
                let key = try EnclaveKey.create(tag: tag)
                var err: NSError?
                let cfgJson = MobileclientPair(invite, Servers.deviceName, key, &err)
                if let err = err { throw err }
                let cfg = (try? JSONSerialization.jsonObject(with: Data(cfgJson.utf8))) as? [String: Any] ?? [:]
                let url = cfg["url"] as? String ?? ""
                let host = URL(string: url)?.host ?? url
                Servers.shared.put(Server(id: cfg["deviceId"] as? String ?? UUID().uuidString, host: host, config: cfgJson,
                                          keyTag: tag, role: cfg["role"] as? String ?? "user"))
                msg = L("Đã ghép đôi với \(host)", "Paired with \(host)")
            } catch {
                EnclaveKey.delete(tag: tag)
                msg = L("Ghép đôi thất bại: ", "Pairing failed: ") + friendly(error)
            }
            DispatchQueue.main.async { busy = false; message = msg }
        }
    }
}

/// One server: status, open, backup switch, remove.
struct ServerRow: View {
    @EnvironmentObject var servers: Servers
    let server: Server
    @State private var status = ""
    @State private var removing = false

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text(server.host).font(.headline).lineLimit(1)
                Spacer()
                Text(server.canWrite ? "ADMIN" : "USER").font(.caption.bold())
                    .foregroundStyle(server.canWrite ? .red : .blue)
            }
            Text(server.revoked ? L("⛔ Đã bị thu hồi trên server", "⛔ Revoked on the server") : status)
                .font(.footnote).foregroundStyle(.secondary)
            HStack {
                if !server.revoked {
                    NavigationLink(L("Mở", "Open")) { BrowseView(serverID: server.id, path: "/") }
                        .buttonStyle(.borderedProminent)
                }
                Spacer()
                Button(L("Xoá", "Remove"), role: .destructive) { removing = true }.buttonStyle(.bordered)
            }
            if !server.revoked && server.canWrite {
                Toggle(L("Tự sao lưu ảnh & video mới", "Back up new photos & videos"), isOn: Binding(
                    get: { server.backup },
                    set: { on in Backup.setEnabled(server.id, on) }))
                if server.backup {
                    Toggle(L("Chỉ khi có Wi-Fi", "Wi-Fi only"), isOn: Binding(
                        get: { server.backupWifiOnly },
                        set: { on in servers.update(server.id) { $0.backupWifiOnly = on } }))
                    Text(Backup.statusText(server.id)).font(.caption).foregroundStyle(.secondary)
                    Button(L("Sao lưu cả ảnh & video cũ", "Also back up older photos & videos")) {
                        servers.update(server.id) { $0.backupSince = 0 }
                        Backup.runSoon()
                    }.font(.footnote)
                }
            }
        }
        .padding(.vertical, 4)
        .task(id: server.revoked) { await refresh() }
        .confirmationDialog(L("Xoá server này khỏi máy?", "Remove this server from this device?"), isPresented: $removing, titleVisibility: .visible) {
            Button(L("Xoá", "Remove"), role: .destructive) { servers.remove(server.id) }
        } message: {
            Text(L("Khoá ghép đôi của máy này sẽ bị xoá; muốn dùng lại phải ghép đôi lại.",
                   "This device's pairing key is deleted; pair again to use it."))
        }
    }

    private func refresh() async {
        guard !server.revoked else { return }
        status = L("Đang kiểm tra…", "Checking…")
        let id = server.id
        let text: String = await Task.detached {
            do {
                switch try Servers.shared.connect(id)["via"] {
                case "lan": return L("● Kết nối qua LAN (nhanh nhất)", "● Connected over the LAN (fastest)")
                case "tailnet": return L("● Kết nối qua tailnet", "● Connected through the tailnet")
                default: return L("● Kết nối qua Internet (Funnel)", "● Connected over the internet (Funnel)")
                }
            } catch {
                return "⚠ " + friendly(error)
            }
        }.value
        status = text
    }
}

/// Language, version, update check.
struct SettingsView: View {
    @EnvironmentObject var model: AppModel
    @Environment(\.dismiss) private var dismiss
    @AppStorage("lang") private var lang = ""
    @State private var note: String?

    var body: some View {
        NavigationStack {
            Form {
                Picker(L("Ngôn ngữ", "Language"), selection: $lang) {
                    Text(L("Theo máy", "Follow the device")).tag("")
                    Text("Tiếng Việt").tag("vi")
                    Text("English").tag("en")
                }
                Section {
                    Button(L("Kiểm tra cập nhật", "Check for updates") + " (v\(AppModel.version))") {
                        note = L("Đang kiểm tra…", "Checking…")
                        model.checkUpdate(force: true) { found, err in
                            note = err.map { L("Không kiểm tra được: ", "Could not check: ") + $0 }
                                ?? (found ? L("Đã có bản mới, xem ở màn hình chính.", "A new version is available, see the home screen.")
                                          : L("Đang dùng bản mới nhất.", "You have the latest version."))
                        }
                    }
                    if let n = note { Text(n).font(.footnote).foregroundStyle(.secondary) }
                } footer: {
                    Text(L("File tải về nằm trong app Tệp: Trên iPhone › NASfone.",
                           "Downloaded files are in the Files app: On My iPhone › NASfone."))
                }
            }
            .navigationTitle(L("Cài đặt", "Settings"))
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("OK") { dismiss() } } }
        }
    }
}
