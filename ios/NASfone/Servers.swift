import Foundation
import UIKit
import Nasfonecore

/// One paired server as the app remembers it. Nothing here is secret.
struct Server: Codable, Identifiable, Equatable {
    var id: String          // the device ID the server gave this phone
    var host: String        // shown name, e.g. nasfone.example.ts.net
    var config: String      // core config JSON (server key, URL, role…)
    var keyTag: String      // Secure Enclave key tag for that server
    var addrs: String = "[]"
    var role: String = "user"
    var revoked: Bool = false
    var backup: Bool = false
    var backupWifiOnly: Bool = true
    var backupSince: Double = 0 // only media created after this (unix seconds)

    var canWrite: Bool { role == "admin" }
}

/// The paired servers and their live sessions (Go core).
final class Servers: ObservableObject {
    static let shared = Servers()

    @Published private(set) var list: [Server] = []
    private var sessions: [String: MobileclientSession] = [:]
    private let lock = NSLock()

    init() { load() }

    private func load() {
        if let d = UserDefaults.standard.data(forKey: "servers"),
           let l = try? JSONDecoder().decode([Server].self, from: d) {
            list = l
        }
    }

    private func save() {
        if let d = try? JSONEncoder().encode(list) { UserDefaults.standard.set(d, forKey: "servers") }
    }

    func get(_ id: String) -> Server? { list.first { $0.id == id } }

    func put(_ s: Server) {
        onMain {
            self.list.removeAll { $0.id == s.id }
            self.list.append(s)
            self.save()
        }
    }

    func update(_ id: String, _ f: @escaping (inout Server) -> Void) {
        onMain {
            if let i = self.list.firstIndex(where: { $0.id == id }) {
                f(&self.list[i])
                self.save()
            }
        }
    }

    func remove(_ id: String) {
        guard let s = get(id) else { return }
        lock.lock(); sessions[id] = nil; lock.unlock()
        EnclaveKey.delete(tag: s.keyTag)
        onMain {
            self.list.removeAll { $0.id == id }
            self.save()
        }
    }

    private func onMain(_ f: @escaping () -> Void) {
        if Thread.isMainThread { f() } else { DispatchQueue.main.sync(execute: f) }
    }

    /// The session for a server, created on first use (does not connect yet).
    func session(_ id: String) throws -> MobileclientSession {
        lock.lock(); defer { lock.unlock() }
        if let s = sessions[id] { return s }
        guard let srv = get(id) else { throw NSError(domain: "NASfone", code: 1, userInfo: [NSLocalizedDescriptionKey: "unknown server"]) }
        var err: NSError?
        guard let s = MobileclientNewSession(srv.config, srv.addrs, EnclaveKey(tag: srv.keyTag), &err) else {
            throw err ?? NSError(domain: "NASfone", code: 2)
        }
        sessions[id] = s
        return s
    }

    /// Signs in if needed (call off the main thread) and remembers what the
    /// server said. Returns {"base","via","role"}.
    @discardableResult
    func connect(_ id: String, fresh: Bool = false) throws -> [String: String] {
        let s = try session(id)
        do {
            let js = try goCall { fresh ? s.reconnect($0) : s.connect($0) }
            let info = (try? JSONSerialization.jsonObject(with: Data(js.utf8))) as? [String: String] ?? [:]
            let addrs = s.addrs()
            update(id) {
                $0.addrs = addrs
                if let r = info["role"], !r.isEmpty { $0.role = r }
                $0.revoked = false
            }
            return info
        } catch {
            if isRevoked(error) { update(id) { $0.revoked = true; $0.backup = false } }
            throw error
        }
    }

    static var deviceName: String {
        let d = UIDevice.current
        return "iOS – \(d.model) \(d.systemVersion)"
    }
}
