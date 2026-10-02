import Foundation
import Photos
import BackgroundTasks
import Network
import UIKit
import Nasfonecore

/// Photo & video backup to every server that has it switched on. New media go
/// to "/Camera Backup/<device>/<yyyy-MM>/<name>"; a file with the same name
/// already there is skipped, so re-runs never duplicate.
///
/// iOS decides when apps may run in the background, so backup runs when the
/// app comes to the foreground and in background processing windows (often
/// while charging on Wi-Fi), resuming where it left off.
enum Backup {
    static let taskID = "com.nasfone.ios.backup"
    private static let queue = DispatchQueue(label: "nasfone.backup")
    private static var running = false

    // MARK: settings

    static func setEnabled(_ id: String, _ on: Bool) {
        if !on {
            Servers.shared.update(id) { $0.backup = false }
            return
        }
        PHPhotoLibrary.requestAuthorization(for: .readWrite) { status in
            let ok = status == .authorized || status == .limited
            Servers.shared.update(id) {
                $0.backup = ok
                if ok && $0.backupSince == 0 { $0.backupSince = Date().timeIntervalSince1970 } // new media from now on
            }
            if ok { runSoon(); schedule() }
            else { setStatus(id, L("Cần quyền truy cập Ảnh để sao lưu (Cài đặt › NASfone › Ảnh).",
                                   "Photo access is needed for backup (Settings › NASfone › Photos).")) }
        }
    }

    static func statusText(_ id: String) -> String {
        UserDefaults.standard.string(forKey: "backupStatus_\(id)") ?? L("Chưa chạy lần nào.", "Not run yet.")
    }

    private static func setStatus(_ id: String, _ s: String) {
        UserDefaults.standard.set(s, forKey: "backupStatus_\(id)")
        DispatchQueue.main.async { Servers.shared.objectWillChange.send() }
    }

    private static var enabled: [Server] { Servers.shared.list.filter { $0.backup && $0.canWrite && !$0.revoked } }

    // MARK: background task

    static func registerBackgroundTask() {
        BGTaskScheduler.shared.register(forTaskWithIdentifier: taskID, using: nil) { task in
            guard let task = task as? BGProcessingTask else { return }
            schedule() // the next window
            var stop = false
            task.expirationHandler = { stop = true }
            queue.async {
                run { stop }
                task.setTaskCompleted(success: true)
            }
        }
    }

    static func schedule() {
        guard !enabled.isEmpty else { return }
        let r = BGProcessingTaskRequest(identifier: taskID)
        r.requiresNetworkConnectivity = true
        r.requiresExternalPower = false
        r.earliestBeginDate = Date(timeIntervalSinceNow: 15 * 60)
        try? BGTaskScheduler.shared.submit(r)
    }

    /// A pass now, while the app is in the foreground (keeps going briefly in the background).
    static func runSoon() {
        guard !enabled.isEmpty else { return }
        queue.async {
            var bg = UIBackgroundTaskIdentifier.invalid
            var stop = false
            DispatchQueue.main.sync {
                bg = UIApplication.shared.beginBackgroundTask { stop = true }
            }
            run { stop }
            DispatchQueue.main.async { UIApplication.shared.endBackgroundTask(bg) }
        }
    }

    // MARK: the pass

    private static func onExpensiveNetwork() -> Bool {
        let m = NWPathMonitor()
        let sem = DispatchSemaphore(value: 0)
        var expensive = false
        m.pathUpdateHandler = { p in expensive = p.isExpensive || p.isConstrained; sem.signal() }
        m.start(queue: DispatchQueue.global())
        _ = sem.wait(timeout: .now() + 2)
        m.cancel()
        return expensive
    }

    private static func run(shouldStop: () -> Bool) {
        if running { return }
        running = true
        defer { running = false }
        let st = PHPhotoLibrary.authorizationStatus(for: .readWrite)
        guard st == .authorized || st == .limited else { return }
        let expensive = onExpensiveNetwork()
        let monthFmt = DateFormatter()
        monthFmt.dateFormat = "yyyy-MM"
        let stamp = DateFormatter()
        stamp.dateFormat = "HH:mm dd/MM"
        let device = UIDevice.current.model.replacingOccurrences(of: "/", with: "_")
        let folder = "/Camera Backup/" + device

        for s in enabled {
            if shouldStop() { return }
            if s.backupWifiOnly && expensive {
                setStatus(s.id, L("Đang chờ Wi-Fi…", "Waiting for Wi-Fi…"))
                continue
            }
            let sess: MobileclientSession
            do {
                try Servers.shared.connect(s.id)
                sess = try Servers.shared.session(s.id)
            } catch {
                setStatus(s.id, "⚠ " + friendly(error))
                continue
            }
            let opts = PHFetchOptions()
            opts.predicate = NSPredicate(format: "creationDate >= %@", Date(timeIntervalSince1970: s.backupSince) as NSDate)
            opts.sortDescriptors = [NSSortDescriptor(key: "creationDate", ascending: true)]
            let assets = PHAsset.fetchAssets(with: opts)
            var done = 0, skipped = 0
            var made = Set<String>()
            for i in 0..<assets.count {
                if shouldStop() {
                    setStatus(s.id, L("Đang sao lưu… còn \(assets.count - i) file", "Backing up… \(assets.count - i) files left"))
                    return
                }
                let a = assets.object(at: i)
                let res = PHAssetResource.assetResources(for: a)
                guard let r = res.first(where: { $0.type == .photo || $0.type == .video || $0.type == .fullSizePhoto || $0.type == .fullSizeVideo }) ?? res.first
                else { continue }
                let created = a.creationDate ?? Date()
                let dir = folder + "/" + monthFmt.string(from: created)
                do {
                    if !made.contains(dir) {
                        var p = ""
                        for part in dir.split(separator: "/") {
                            p += "/" + part
                            try? sess.mkdir(p) // "already exists" is fine
                        }
                        made.insert(dir)
                    }
                    let tmp = LocalFiles.cache.appendingPathComponent(UUID().uuidString + "-" + r.originalFilename)
                    try export(r, to: tmp)
                    defer { try? FileManager.default.removeItem(at: tmp) }
                    let h = try FileHandle(forReadingFrom: tmp)
                    let fd = dup(h.fileDescriptor)
                    try? h.close()
                    let size = (try? FileManager.default.attributesOfItem(atPath: tmp.path)[.size] as? Int64) ?? -1
                    let written = try goCall { sess.upload(dir + "/" + r.originalFilename, fd: Int(fd), size: size,
                                                              mode: MobileclientModeSkip, progress: nil, error: $0) }
                    if written.isEmpty { skipped += 1 } else { done += 1 }
                    Servers.shared.update(s.id) { $0.backupSince = created.timeIntervalSince1970 } // resume point
                } catch {
                    setStatus(s.id, "⚠ \(r.originalFilename): " + friendly(error))
                    if isRevoked(error) { break }
                    return // network trouble: retry later from this file
                }
            }
            setStatus(s.id, L("Lần cuối: \(stamp.string(from: Date())) • tải lên \(done), đã có \(skipped)",
                              "Last run: \(stamp.string(from: Date())) • uploaded \(done), already there \(skipped)"))
        }
    }

    /// Writes a photo/video resource to a file (downloads it from iCloud if needed).
    private static func export(_ r: PHAssetResource, to url: URL) throws {
        let opts = PHAssetResourceRequestOptions()
        opts.isNetworkAccessAllowed = true
        let sem = DispatchSemaphore(value: 0)
        var failure: Error?
        PHAssetResourceManager.default().writeData(for: r, toFile: url, options: opts) { err in
            failure = err
            sem.signal()
        }
        sem.wait()
        if let e = failure { throw e }
    }
}
