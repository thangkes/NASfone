import Foundation

/// UI language: the in-app choice ("vi" / "en"), else the phone's language.
enum Lang {
    static var vi: Bool {
        switch UserDefaults.standard.string(forKey: "lang") ?? "" {
        case "vi": return true
        case "en": return false
        default: return (Locale.preferredLanguages.first ?? "").hasPrefix("vi")
        }
    }
}

/// Picks the Vietnamese or English text for the current UI language.
func L(_ vi: String, _ en: String) -> String { Lang.vi ? vi : en }

func humanSize(_ n: Int64) -> String {
    if n < 0 { return "?" }
    let f = ByteCountFormatter()
    f.countStyle = .file
    return f.string(fromByteCount: n)
}

func shortDate(_ ms: Int64) -> String {
    if ms <= 0 { return "" }
    let f = DateFormatter()
    f.dateFormat = "HH:mm dd/MM/yyyy"
    return f.string(from: Date(timeIntervalSince1970: TimeInterval(ms) / 1000))
}

/// A friendly message for errors from the Go core.
func friendly(_ e: Error) -> String {
    let m = (e as NSError).localizedDescription
    if m.hasPrefix("revoked:") { return L("Server đã thu hồi máy này. Hãy ghép đôi lại.", "The server revoked this device. Pair it again.") }
    if m.hasPrefix("readonly:") { return L("Máy này chỉ có quyền xem và tải về.", "This device has view and download access only.") }
    if m.hasPrefix("notfound:") { return L("Không tìm thấy (có thể đã bị xoá).", "Not found (it may have been deleted).") }
    if m.contains("no address") || m.lowercased().contains("timeout") || m.contains("dial tcp") || m.contains("no such host") {
        return L("Không kết nối được tới server. Kiểm tra mạng, hoặc server đang tắt.",
                 "Cannot reach the server. Check the network or whether the server is running.")
    }
    return m
}

func isRevoked(_ e: Error) -> Bool { (e as NSError).localizedDescription.hasPrefix("revoked:") }
