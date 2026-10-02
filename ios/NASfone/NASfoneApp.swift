import SwiftUI
import BackgroundTasks
import Nasfonecore

@main
struct NASfoneApp: App {
    @StateObject private var servers = Servers.shared
    @StateObject private var model = AppModel()
    @Environment(\.scenePhase) private var phase

    init() {
        Backup.registerBackgroundTask()
    }

    var body: some Scene {
        WindowGroup {
            HomeView()
                .environmentObject(servers)
                .environmentObject(model)
                .onOpenURL { url in model.pendingInvite = url.absoluteString } // nasfone://pair?i=…
        }
        .onChange(of: phase) { p in
            if p == .active {
                Backup.runSoon()
                model.checkUpdate()
            } else if p == .background {
                Backup.schedule()
            }
        }
    }
}

/// App-wide state that is not about one server.
final class AppModel: ObservableObject {
    @Published var pendingInvite: String?
    @Published var update: [String: Any]?
    private var checkedAt = Date.distantPast

    static var version: String {
        Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "dev"
    }

    /// Looks for a newer iOS build on GitHub (at most every 6 hours).
    func checkUpdate(force: Bool = false, done: ((Bool, String?) -> Void)? = nil) {
        if !force && Date().timeIntervalSince(checkedAt) < 6 * 3600 { return }
        checkedAt = Date()
        DispatchQueue.global().async {
            var err: NSError?
            let js = MobileclientCheckUpdate(Self.version, &err)
            let rel = js.isEmpty ? nil : (try? JSONSerialization.jsonObject(with: Data(js.utf8))) as? [String: Any]
            DispatchQueue.main.async {
                self.update = rel
                done?(rel != nil, err?.localizedDescription)
            }
        }
    }
}
