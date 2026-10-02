import SwiftUI
import UIKit
import PhotosUI
import QuickLook
import UniformTypeIdentifiers
import Nasfonecore

struct Entry: Codable, Identifiable, Hashable {
    var name: String
    var path: String
    var dir: Bool
    var size: Int64
    var mtime: Int64
    var mime: String
    var id: String { path }
}

/// Reports transfer progress from the Go core to the UI.
final class ProgressSink: NSObject, MobileclientProgressProtocol {
    let onChange: (Int64, Int64) -> Void
    init(_ f: @escaping (Int64, Int64) -> Void) { onChange = f }
    func onProgress(_ done: Int64, total: Int64) { onChange(done, total) }
}

/// Folder where downloads are kept (visible in the Files app).
enum LocalFiles {
    static var downloads: URL {
        let d = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        return d
    }
    static var cache: URL {
        let c = FileManager.default.temporaryDirectory.appendingPathComponent("nasfone-open", isDirectory: true)
        try? FileManager.default.createDirectory(at: c, withIntermediateDirectories: true)
        return c
    }
}

/// Browse one folder of one server.
struct BrowseView: View {
    let serverID: String
    let path: String
    @EnvironmentObject var servers: Servers
    @State private var entries: [Entry] = []
    @State private var status = ""
    @State private var loading = true
    @State private var error: String?
    @State private var preview: URL?
    @State private var share: URL?
    @State private var pickPhotos = false
    @State private var pickFiles = false
    @State private var newFolder = false
    @State private var folderName = ""
    @State private var renaming: Entry?
    @State private var newName = ""
    @State private var deleting: Entry?
    @State private var toast: String?
    @StateObject private var uploader = Uploader()

    private var server: Server? { servers.get(serverID) }
    private var canWrite: Bool { server?.canWrite ?? false }

    var body: some View {
        List {
            if !status.isEmpty { Text(status).font(.footnote).foregroundStyle(.secondary) }
            if let e = error {
                Text("⚠ " + e).foregroundStyle(.red)
                Button(L("Thử lại", "Retry")) { Task { await load() } }
            }
            if !loading && error == nil && entries.isEmpty {
                Text(L("Thư mục trống.", "This folder is empty.")).foregroundStyle(.secondary)
            }
            ForEach(entries) { e in
                if e.dir {
                    NavigationLink { BrowseView(serverID: serverID, path: e.path) } label: { row(e) }
                        .contextMenu { menu(e) }
                } else {
                    Button { open(e) } label: { row(e) }
                        .contextMenu { menu(e) }
                }
            }
        }
        .navigationTitle(path == "/" ? (server?.host ?? "NASfone") : (path as NSString).lastPathComponent)
        .navigationBarTitleDisplayMode(.inline)
        .refreshable { await load() }
        .task { await load() }
        .toolbar {
            if canWrite {
                ToolbarItem(placement: .topBarTrailing) {
                    Menu {
                        Button { pickPhotos = true } label: { Label(L("Tải lên ảnh & video", "Upload photos & videos"), systemImage: "photo.on.rectangle") }
                        Button { pickFiles = true } label: { Label(L("Tải lên file", "Upload files"), systemImage: "doc") }
                        Button { folderName = ""; newFolder = true } label: { Label(L("Thư mục mới", "New folder"), systemImage: "folder.badge.plus") }
                    } label: { Image(systemName: "plus.circle") }
                }
            }
        }
        .overlay(alignment: .bottom) {
            if let t = uploader.progress ?? toast {
                Text(t).font(.footnote).padding(10).background(.regularMaterial).clipShape(Capsule()).padding()
            }
        }
        .sheet(isPresented: $pickPhotos) {
            PhotoPicker { urls in uploader.start(urls, to: path, server: serverID) { reload() } }
        }
        .fileImporter(isPresented: $pickFiles, allowedContentTypes: [.item], allowsMultipleSelection: true) { r in
            if case .success(let urls) = r { uploader.start(urls, to: path, server: serverID, scoped: true) { reload() } }
        }
        .confirmationDialog(uploader.conflictTitle, isPresented: $uploader.asking, titleVisibility: .visible) {
            Button(L("Ghi đè", "Overwrite")) { uploader.answer(MobileclientModeOverwrite) }
            Button(L("Giữ cả hai", "Keep both")) { uploader.answer(MobileclientModeKeepBoth) }
            Button(L("Bỏ qua", "Skip"), role: .cancel) { uploader.answer(MobileclientModeSkip) }
        }
        .alert(L("Thư mục mới", "New folder"), isPresented: $newFolder) {
            TextField(L("Tên thư mục", "Folder name"), text: $folderName)
            Button("OK") { act(L("Đã tạo thư mục", "Folder created")) { try $0.mkdir(join(path, folderName)) } }
            Button(L("Huỷ", "Cancel"), role: .cancel) {}
        }
        .alert(L("Đổi tên", "Rename"), isPresented: Binding(get: { renaming != nil }, set: { if !$0 { renaming = nil } })) {
            TextField("", text: $newName)
            Button("OK") {
                if let e = renaming { act(L("Đã đổi tên", "Renamed")) { try $0.move(e.path, dest: join(path, newName)) } }
            }
            Button(L("Huỷ", "Cancel"), role: .cancel) {}
        }
        .confirmationDialog(L("Xoá \"\(deleting?.name ?? "")\"?", "Delete \"\(deleting?.name ?? "")\"?"),
                            isPresented: Binding(get: { deleting != nil }, set: { if !$0 { deleting = nil } }), titleVisibility: .visible) {
            Button(L("Xoá", "Delete"), role: .destructive) {
                if let e = deleting { act(L("Đã xoá", "Deleted")) { try $0.delete(e.path) } }
            }
        }
        .quickLookPreview($preview)
        .sheet(item: Binding(get: { share.map { ShareItem(url: $0) } }, set: { share = $0?.url })) { item in
            ShareSheet(items: [item.url])
        }
    }

    private func row(_ e: Entry) -> some View {
        HStack(spacing: 12) {
            Image(systemName: icon(e)).font(.title2).frame(width: 32).foregroundStyle(e.dir ? .yellow : .accentColor)
            VStack(alignment: .leading) {
                Text(e.name).foregroundStyle(.primary).lineLimit(2)
                Text(e.dir ? shortDate(e.mtime) : humanSize(e.size) + " • " + shortDate(e.mtime))
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
    }

    private func icon(_ e: Entry) -> String {
        if e.dir { return "folder.fill" }
        if e.mime.hasPrefix("image/") { return "photo" }
        if e.mime.hasPrefix("video/") { return "film" }
        if e.mime.hasPrefix("audio/") { return "music.note" }
        if e.mime == "application/pdf" { return "doc.richtext" }
        return "doc"
    }

    @ViewBuilder private func menu(_ e: Entry) -> some View {
        if !e.dir {
            Button { open(e) } label: { Label(L("Xem", "View"), systemImage: "eye") }
            Button { fetch(e) { share = $0 } } label: { Label(L("Chia sẻ / Lưu…", "Share / Save…"), systemImage: "square.and.arrow.up") }
            Button { saveToDevice(e) } label: { Label(L("Tải về máy", "Download to device"), systemImage: "arrow.down.circle") }
        }
        if canWrite {
            Button { newName = e.name; renaming = e } label: { Label(L("Đổi tên", "Rename"), systemImage: "pencil") }
            Button(role: .destructive) { deleting = e } label: { Label(L("Xoá", "Delete"), systemImage: "trash") }
        }
    }

    private func join(_ dir: String, _ name: String) -> String { (dir.hasSuffix("/") ? dir : dir + "/") + name }

    private func reload() { Task { await load() } }

    private func load() async {
        loading = true
        let id = serverID, p = path
        let r: Result<([Entry], String), Error> = await Task.detached {
            do {
                let info = try Servers.shared.connect(id)
                let s = try Servers.shared.session(id)
                let list = try JSONDecoder().decode([Entry].self, from: Data(try s.list(p).utf8))
                var line = L("Qua ", "Via ") + (info["via"] == "lan" ? "LAN" : info["via"] == "tailnet" ? "tailnet" : "Internet")
                if let q = try? JSONSerialization.jsonObject(with: Data(try s.quota().utf8)) as? [String: Int64],
                   let a = q["avail"], a >= 0 {
                    line += " • " + L("trống ", "free ") + humanSize(a)
                }
                return .success((list, line))
            } catch {
                return .failure(error)
            }
        }.value
        loading = false
        switch r {
        case .success(let (list, line)):
            entries = list; error = nil
            status = line + " • " + (canWrite ? "ADMIN" : L("USER (chỉ xem)", "USER (view only)"))
        case .failure(let e):
            error = friendly(e)
        }
    }

    /// Downloads a file to a local copy, then hands it over.
    private func fetch(_ e: Entry, into dir: URL = LocalFiles.cache, then: @escaping (URL) -> Void) {
        let id = serverID
        toast = L("Đang tải \(e.name)…", "Downloading \(e.name)…")
        Task.detached {
            let dest = dir.appendingPathComponent(e.name)
            do {
                FileManager.default.createFile(atPath: dest.path, contents: nil)
                let h = try FileHandle(forWritingTo: dest)
                let fd = dup(h.fileDescriptor) // the core closes its copy
                try? h.close()
                let sink = ProgressSink { done, total in
                    DispatchQueue.main.async {
                        toast = total > 0 ? "\(e.name) \(done * 100 / total)%" : "\(e.name) \(humanSize(done))"
                    }
                }
                try Servers.shared.session(id).download(e.path, fd: Int(fd), progress: sink)
                await MainActor.run { toast = nil; then(dest) }
            } catch {
                try? FileManager.default.removeItem(at: dest)
                await MainActor.run { toast = friendly(error) }
            }
        }
    }

    private func open(_ e: Entry) { fetch(e) { preview = $0 } }

    private func saveToDevice(_ e: Entry) {
        fetch(e, into: LocalFiles.downloads) { _ in
            toast = L("Đã lưu vào Tệp › Trên iPhone › NASfone", "Saved to Files › On My iPhone › NASfone")
        }
    }

    private func act(_ done: String, _ f: @escaping (MobileclientSession) throws -> Void) {
        let id = serverID
        Task.detached {
            var msg = done
            do { try f(try Servers.shared.session(id)) } catch { msg = friendly(error) }
            await MainActor.run { toast = msg }
            await load()
        }
    }
}

struct ShareItem: Identifiable { let url: URL; var id: String { url.path } }

struct ShareSheet: UIViewControllerRepresentable {
    let items: [Any]
    func makeUIViewController(context: Context) -> UIActivityViewController {
        UIActivityViewController(activityItems: items, applicationActivities: nil)
    }
    func updateUIViewController(_ c: UIActivityViewController, context: Context) {}
}

/// Picks photos and videos and hands back local file copies.
struct PhotoPicker: UIViewControllerRepresentable {
    let onPicked: ([URL]) -> Void

    func makeUIViewController(context: Context) -> PHPickerViewController {
        var cfg = PHPickerConfiguration(photoLibrary: .shared())
        cfg.selectionLimit = 0
        cfg.preferredAssetRepresentationMode = .current
        let p = PHPickerViewController(configuration: cfg)
        p.delegate = context.coordinator
        return p
    }

    func updateUIViewController(_ c: PHPickerViewController, context: Context) {}

    func makeCoordinator() -> Coordinator { Coordinator(onPicked) }

    final class Coordinator: NSObject, PHPickerViewControllerDelegate {
        let onPicked: ([URL]) -> Void
        init(_ f: @escaping ([URL]) -> Void) { onPicked = f }

        func picker(_ picker: PHPickerViewController, didFinishPicking results: [PHPickerResult]) {
            picker.dismiss(animated: true)
            let group = DispatchGroup()
            var urls: [URL] = []
            let lock = NSLock()
            for r in results {
                let p = r.itemProvider
                guard let type = p.registeredTypeIdentifiers.first else { continue }
                group.enter()
                p.loadFileRepresentation(forTypeIdentifier: type) { url, _ in
                    defer { group.leave() }
                    guard let url = url else { return }
                    // The provided file disappears after this callback: copy it.
                    let dest = LocalFiles.cache.appendingPathComponent(url.lastPathComponent)
                    try? FileManager.default.removeItem(at: dest)
                    if (try? FileManager.default.copyItem(at: url, to: dest)) != nil {
                        lock.lock(); urls.append(dest); lock.unlock()
                    }
                }
            }
            group.notify(queue: .main) { self.onPicked(urls) }
        }
    }
}

/// Uploads files one by one, asking what to do when a name already exists.
final class Uploader: ObservableObject {
    @Published var progress: String?
    @Published var asking = false
    @Published var conflictTitle = ""
    private var choice: String?
    private var sticky: String?
    private let sem = DispatchSemaphore(value: 0)

    func answer(_ mode: String) {
        choice = mode
        sem.signal()
    }

    func start(_ urls: [URL], to dir: String, server: String, scoped: Bool = false, done: @escaping () -> Void) {
        guard !urls.isEmpty else { return }
        sticky = nil
        DispatchQueue.global().async {
            var ok = 0, skipped = 0, failed = 0
            for (i, url) in urls.enumerated() {
                let access = scoped && url.startAccessingSecurityScopedResource()
                defer { if access { url.stopAccessingSecurityScopedResource() } }
                let name = url.lastPathComponent
                let target = (dir.hasSuffix("/") ? dir : dir + "/") + name
                let label = L("Tải lên", "Uploading") + " \(i + 1)/\(urls.count): \(name)"
                let size = (try? FileManager.default.attributesOfItem(atPath: url.path)[.size] as? Int64) ?? -1
                func send(_ mode: String) throws -> String {
                    let h = try FileHandle(forReadingFrom: url)
                    let fd = dup(h.fileDescriptor)
                    try? h.close()
                    let sink = ProgressSink { d, t in
                        DispatchQueue.main.async { self.progress = t > 0 ? "\(label) \(d * 100 / t)%" : label }
                    }
                    return try Servers.shared.session(server).upload(target, fd: Int(fd), size: size, mode: mode, progress: sink)
                }
                do {
                    var result: String
                    do {
                        result = try send(self.sticky ?? MobileclientModeFail)
                    } catch where (error as NSError).localizedDescription.hasPrefix("exists:") {
                        DispatchQueue.main.async {
                            self.conflictTitle = L("\"\(name)\" đã có trên server", "\"\(name)\" already exists on the server")
                            self.asking = true
                        }
                        self.sem.wait()
                        let mode = self.choice ?? MobileclientModeSkip
                        result = mode == MobileclientModeSkip ? "" : try send(mode)
                    }
                    if result.isEmpty { skipped += 1 } else { ok += 1 }
                } catch {
                    failed += 1
                }
                if scoped == false { try? FileManager.default.removeItem(at: url) } // picker copies
            }
            DispatchQueue.main.async {
                var msg = L("Đã tải lên \(ok) file", "Uploaded \(ok) file(s)")
                if skipped > 0 { msg += L(", bỏ qua \(skipped)", ", skipped \(skipped)") }
                if failed > 0 { msg += L(", lỗi \(failed)", ", failed \(failed)") }
                self.progress = msg
                done()
                DispatchQueue.main.asyncAfter(deadline: .now() + 3) { self.progress = nil }
            }
        }
    }
}
