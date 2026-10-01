package com.nasfone.server

import android.Manifest
import android.app.Activity
import android.app.AlertDialog
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.graphics.Typeface
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.os.Handler
import android.os.Looper
import android.os.PowerManager
import android.provider.Settings
import android.text.InputType
import android.util.TypedValue
import android.view.Gravity
import android.view.View
import android.view.ViewGroup.LayoutParams.MATCH_PARENT
import android.view.ViewGroup.LayoutParams.WRAP_CONTENT
import android.view.WindowInsets
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.ScrollView
import android.widget.Switch
import android.widget.TextView
import android.widget.Toast
import com.nasfone.core.mobile.Mobile
import org.json.JSONArray
import org.json.JSONObject
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlin.concurrent.thread

class MainActivity : Activity() {
    private lateinit var prefs: Prefs

    private lateinit var stateTv: TextView
    private lateinit var accountBox: LinearLayout
    private lateinit var addrBox: LinearLayout
    private lateinit var toggleBtn: Button
    private lateinit var loginBtn: Button
    private lateinit var logoutBtn: Button
    private lateinit var funnelSw: Switch
    private lateinit var funnelTv: TextView
    private lateinit var funnelHelpBtn: Button
    private lateinit var hostEt: EditText
    private lateinit var controlEt: EditText
    private lateinit var rootEt: EditText
    private lateinit var verboseCb: CheckBox
    private lateinit var autoCb: CheckBox
    private lateinit var permTv: TextView
    private lateinit var logTv: TextView
    private lateinit var adminCodeTv: TextView
    private lateinit var userCodeTv: TextView
    private lateinit var adminCopyBtn: Button
    private lateinit var userCopyBtn: Button
    private lateinit var codeInfoTv: TextView
    private lateinit var codeBar: ProgressBar
    private lateinit var devBox: LinearLayout

    private var adminCode: String? = null
    private var userCode: String? = null
    private var lastDevicesJson = ""
    private lateinit var pairedBox: LinearLayout
    private var lastPairedJson = ""
    private val ticker = Handler(Looper.getMainLooper())
    private val tick = object : Runnable {
        override fun run() {
            // Lỗi hiển thị không được làm sập app (service vẫn chạy trong cùng tiến trình).
            try {
                renderCode()
                renderDevices()
                renderPaired()
            } catch (e: Exception) {
                Core.log(L("Lỗi hiển thị: $e", "Display error: $e"))
            }
            ticker.postDelayed(this, 1000)
        }
    }
    private val dateFmt = SimpleDateFormat("HH:mm dd/MM", Locale.US)

    private val onChange: () -> Unit = { render() }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        prefs = Prefs(this)
        Lang.init(this)
        setContentView(buildUi())
        // Mở app là có mã đăng nhập: tự khởi động server nếu đang dừng.
        if (!Core.running && savedInstanceState == null) toggle()
        if (Build.VERSION.SDK_INT >= 33 &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1)
        }
    }

    override fun onResume() {
        super.onResume()
        Core.addListener(onChange)
        render()
        ticker.post(tick)
    }

    override fun onPause() {
        Core.removeListener(onChange)
        ticker.removeCallbacks(tick)
        super.onPause()
    }

    // ---------------------------------------------------------------- UI

    private fun dp(v: Int) = TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, v.toFloat(), resources.displayMetrics).toInt()

    private fun buildUi(): View {
        val col = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(16), dp(12), dp(16), dp(24))
        }
        val scroll = ScrollView(this).apply { addView(col) }
        scroll.setOnApplyWindowInsetsListener { v, insets ->
            val bars = insets.getInsets(WindowInsets.Type.systemBars() or WindowInsets.Type.ime())
            v.setPadding(bars.left, bars.top, bars.right, bars.bottom)
            insets
        }

        // Tên app đã có trên thanh tiêu đề của hệ thống; không lặp lại ở đây.
        stateTv = text("", 16f, bold = true).also { col.addView(it) }
        toggleBtn = button("") { toggle() }.also { col.addView(it) }

        section(col, L("Mã đăng nhập web (6 số, đổi mỗi phút)", "Web sign-in codes (6 digits, change every minute)"))
        // Hai mã luôn khác nhau; màu và nhãn tách biệt để không đưa nhầm mã Admin.
        val (aTv, aBtn) = codeBlock(col, L("ADMIN — toàn quyền (tải lên, ghi đè, xóa)", "ADMIN — full access (upload, overwrite, delete)"), "#B42318") { adminCode }
        adminCodeTv = aTv; adminCopyBtn = aBtn
        val (uTv, uBtn) = codeBlock(col, L("USER — chỉ xem & tải về", "USER — view & download only"), "#2F6FED") { userCode }
        userCodeTv = uTv; userCopyBtn = uBtn
        codeBar = ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal).apply { max = 60 }
            .also { col.addView(it, LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT)) }
        codeInfoTv = text("", 13f).apply { gravity = Gravity.CENTER }
            .also { col.addView(it, LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT)) }

        section(col, L("Ứng dụng đã ghép (Windows / Android)", "Paired apps (Windows / Android)"))
        pairedBox = vbox().also { col.addView(it) }
        col.addView(button(L("＋ Ghép thiết bị mới", "＋ Pair new device")) { newPairing() })

        section(col, L("Phiên trình duyệt (đăng nhập bằng mã 6 số)", "Browser sessions (signed in with a 6-digit code)"))
        devBox = vbox().also { col.addView(it) }
        col.addView(button(L("Thu hồi tất cả", "Revoke all")) {
            AlertDialog.Builder(this)
                .setTitle(L("Thu hồi tất cả thiết bị?", "Revoke all devices?"))
                .setMessage(L("Mọi trình duyệt đang đăng nhập sẽ bị đăng xuất ngay.", "Every signed-in browser is signed out immediately."))
                .setPositiveButton(L("Thu hồi", "Revoke")) { _, _ -> thread { Mobile.revokeAllDevices(); runOnUiThread { lastDevicesJson = ""; renderDevices() } } }
                .setNegativeButton(L("Hủy", "Cancel"), null)
                .show()
        })

        section(col, L("Tài khoản Tailscale", "Tailscale account"))
        accountBox = vbox().also { col.addView(it) }
        val accBtns = hbox().also { col.addView(it) }
        loginBtn = button(L("Đăng nhập", "Sign in")) { login() }.also { accBtns.addView(it, weighted()) }
        logoutBtn = button(L("Đăng xuất / đổi tài khoản", "Sign out / switch account")) { logout() }.also { accBtns.addView(it, weighted()) }

        section(col, L("Địa chỉ truy cập", "Addresses"))
        addrBox = vbox().also { col.addView(it) }

        section(col, L("Funnel (truy cập công khai qua Internet)", "Funnel (public access over the internet)"))
        funnelSw = Switch(this).apply {
            text = L("Bật Funnel", "Enable Funnel")
            isChecked = prefs.funnel
            setOnCheckedChangeListener { _, on ->
                prefs.funnel = on
                if (Core.running) thread { Mobile.setFunnel(on) }
            }
        }
        col.addView(funnelSw)
        funnelTv = text("", 13f).also { col.addView(it) }
        funnelHelpBtn = button(L("Mở trang cài đặt Tailscale để sửa", "Open the Tailscale settings page to fix this")) {
            Core.status.optString("funnelHelpURL").takeIf { it.isNotEmpty() }?.let {
                startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(it)))
            }
        }.also { col.addView(it) }

        section(col, L("Cài đặt (áp dụng khi khởi động lại)", "Settings (applied on restart)"))
        hostEt = field(col, L("Tên máy trong tailnet", "Machine name in the tailnet"), prefs.hostname)
        controlEt = field(col, L("Máy chủ điều khiển (để trống = Tailscale; điền URL nếu dùng Headscale)", "Control server (empty = Tailscale; enter a URL for Headscale)"), prefs.controlUrl)
        rootEt = field(col, L("Thư mục lưu trữ", "Storage folder"), prefs.rootDir)
        verboseCb = CheckBox(this).apply { text = L("Ghi log chi tiết của Tailscale", "Verbose Tailscale logs"); isChecked = prefs.verboseLog }.also { col.addView(it) }
        autoCb = CheckBox(this).apply {
            text = L("Tự chạy khi khởi động máy", "Start when the phone boots")
            isChecked = prefs.autoStart
            setOnCheckedChangeListener { _, on -> prefs.autoStart = on }
        }.also { col.addView(it) }
        col.addView(button(L("Lưu cài đặt", "Save settings")) { save() })
        col.addView(button(L("Ngôn ngữ: ", "Language: ") + when (prefs.lang) {
            "vi" -> "Tiếng Việt"; "en" -> "English"; else -> L("theo máy", "follow the phone")
        }) { chooseLanguage() })

        section(col, L("Quyền & chống bị tắt nền (MagicOS)", "Permissions & background survival"))
        permTv = text("", 14f).also { col.addView(it) }
        col.addView(button(L("Cấp quyền truy cập tất cả file", "Grant all-files access")) {
            startActivity(Intent(Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION, Uri.parse("package:$packageName")))
        })
        col.addView(button(L("Bỏ tối ưu pin", "Disable battery optimization")) {
            @Suppress("BatteryLife")
            startActivity(Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:$packageName")))
        })
        col.addView(button(L("Mở cài đặt ứng dụng (Khởi chạy ứng dụng → thủ công)", "Open app settings (App launch → manage manually)")) {
            startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:$packageName")))
        })

        section(col, L("Nhật ký", "Log"))
        logTv = text("", 11f).apply { typeface = Typeface.MONOSPACE; setTextIsSelectable(true) }.also { col.addView(it) }
        col.addView(button(L("Sao chép nhật ký", "Copy log")) { copy(Core.logLines().joinToString("\n")) })
        return scroll
    }

    private fun text(s: String, size: Float, bold: Boolean = false) = TextView(this).apply {
        text = s
        textSize = size
        if (bold) setTypeface(typeface, Typeface.BOLD)
        setPadding(0, dp(4), 0, dp(4))
    }

    private fun button(label: String, onClick: () -> Unit) = Button(this).apply {
        text = label
        isAllCaps = false
        setOnClickListener { onClick() }
    }

    private fun vbox() = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
    private fun hbox() = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
    private fun weighted() = LinearLayout.LayoutParams(0, WRAP_CONTENT, 1f)

    private fun section(col: LinearLayout, title: String) {
        col.addView(text(title, 13f, bold = true).apply {
            setPadding(0, dp(18), 0, dp(2))
            setTextColor(Color.parseColor("#2F6FED"))
        })
    }

    private fun field(col: LinearLayout, label: String, value: String, type: Int = InputType.TYPE_CLASS_TEXT): EditText {
        col.addView(text(label, 12f).apply { setPadding(0, dp(8), 0, 0) })
        return EditText(this).apply {
            setText(value)
            inputType = type
            setSingleLine()
        }.also { col.addView(it, LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT)) }
    }

    /** Nhãn quyền + mã lớn + nút sao chép cho một loại mã đăng nhập. */
    private fun codeBlock(col: LinearLayout, label: String, color: String, current: () -> String?): Pair<TextView, Button> {
        col.addView(text(label, 13f, bold = true).apply {
            setTextColor(Color.parseColor(color))
            setPadding(0, dp(10), 0, 0)
        })
        val row = hbox().apply { gravity = Gravity.CENTER_VERTICAL }.also { col.addView(it) }
        val tv = text("— — —", 30f, bold = true).apply {
            typeface = Typeface.create(Typeface.MONOSPACE, Typeface.BOLD)
            letterSpacing = 0.12f
            setTextColor(Color.parseColor(color))
        }.also { row.addView(it, weighted()) }
        val btn = button(L("Sao chép", "Copy")) { current()?.let { copy(it.replace(" ", "")) } }.also { row.addView(it) }
        return tv to btn
    }

    /** Một dòng "nhãn: giá trị" kèm nút sao chép. */
    private fun row(label: String, value: String) = hbox().apply {
        gravity = Gravity.CENTER_VERTICAL
        addView(text("$label\n$value", 14f).apply { setTextIsSelectable(true) }, weighted())
        addView(button(L("Sao chép", "Copy")) { copy(value) })
    }

    // ---------------------------------------------------------------- render

    // Last rendered content per section: status updates arrive every ~2 s
    // (traffic counters), so sections are rebuilt only when what they show changed.
    private var accountKey = ""
    private var addrKey = ""
    private var logKey = ""

    private fun render() {
        val st = Core.status
        val backend = st.optString("backendState")
        val running = Core.running

        stateTv.text = when {
            !running && Core.startError != null -> L("⛔ Lỗi: ${Core.startError}", "⛔ Error: ${Core.startError}")
            !running -> L("○ Đã dừng", "○ Stopped")
            backend == "Running" -> L("● Đang chạy • tailnet đã kết nối", "● Running • tailnet connected")
            backend == "NeedsLogin" -> L("● Đang chạy • chờ đăng nhập Tailscale", "● Running • waiting for Tailscale sign-in")
            backend.isEmpty() -> L("● Đang khởi động…", "● Starting…")
            else -> L("● Đang chạy • $backend", "● Running • $backend")
        }
        stateTv.setTextColor(Color.parseColor(if (running) "#1F9D55" else "#D64545"))
        toggleBtn.text = if (running) L("Dừng server", "Stop server") else L("Khởi động server", "Start server")

        val newAccountKey = listOf(running, backend, st.optString("loginName"), st.optString("tailnetName"), st.optString("authURL")).joinToString("|")
        if (newAccountKey != accountKey) {
            accountKey = newAccountKey
            renderAccount(st, backend, running)
        }
        loginBtn.isEnabled = running && backend != "Running"
        logoutBtn.isEnabled = running && backend == "Running"

        val newAddrKey = listOf(running, st.optString("dnsName"), st.optJSONArray("tailscaleIPs")?.toString(), st.optString("funnelURL")).joinToString("|")
        if (newAddrKey != addrKey) {
            addrKey = newAddrKey
            renderAddresses(st, running)
        }
        funnelTv.text = when {
            st.optString("funnelError").isNotEmpty() ->
                "⚠ ${st.optString("funnelError")}\n" + L("App tự thử lại mỗi 20 giây sau khi bạn sửa.", "The app retries every 20 seconds after you fix it.")
            st.optString("funnelURL").isNotEmpty() -> L("Đang mở công khai: ", "Public at: ") + st.optString("funnelURL")
            prefs.funnel -> L("Sẽ mở khi tailnet kết nối xong.", "Opens once the tailnet is connected.")
            else -> L("Tắt — chỉ truy cập qua LAN và tailnet.", "Off — reachable only through the tailnet.")
        }
        funnelHelpBtn.visibility = if (st.optString("funnelHelpURL").isNotEmpty()) View.VISIBLE else View.GONE

        val files = Environment.isExternalStorageManager()
        val battery = (getSystemService(POWER_SERVICE) as PowerManager).isIgnoringBatteryOptimizations(packageName)
        permTv.text = "${if (files) "✔" else "✘"} " + L("Truy cập tất cả file", "All files access") +
            "\n${if (battery) "✔" else "✘"} " + L("Bỏ tối ưu pin", "Battery optimization disabled")

        val logText = Core.logLines().takeLast(80).joinToString("\n")
        if (logText != logKey) {
            logKey = logText
            logTv.text = logText
        }
    }

    private fun renderAccount(st: JSONObject, backend: String, running: Boolean) {
        accountBox.removeAllViews()
        if (running) {
            val login = st.optString("loginName")
            val tailnet = st.optString("tailnetName")
            accountBox.addView(text(
                when {
                    login.isNotEmpty() -> L("Tài khoản: $login\nTailnet: $tailnet", "Account: $login\nTailnet: $tailnet")
                    backend == "NeedsLogin" -> L("Chưa đăng nhập. Bấm \"Đăng nhập\" để mở trang đăng nhập Tailscale.", "Not signed in. Tap \"Sign in\" to open the Tailscale sign-in page.")
                    else -> "…"
                }, 14f
            ))
            val auth = st.optString("authURL")
            if (auth.isNotEmpty()) accountBox.addView(row(L("Link đăng nhập (mở trên máy này hoặc máy khác)", "Sign-in link (open on this or another device)"), auth))
        } else {
            accountBox.addView(text(L("Khởi động server để đăng nhập.", "Start the server to sign in."), 14f))
        }
    }

    private fun renderAddresses(st: JSONObject, running: Boolean) {
        addrBox.removeAllViews()
        if (running) {
            val dns = st.optString("dnsName")
            if (dns.isNotEmpty()) addrBox.addView(row("Tailnet", "http://$dns"))
            val ips = st.optJSONArray("tailscaleIPs")
            if (ips != null && ips.length() > 0) addrBox.addView(row("Tailnet IP", "http://${ips.getString(0)}"))
            val funnel = st.optString("funnelURL")
            if (funnel.isNotEmpty()) addrBox.addView(row(L("Funnel (công khai)", "Funnel (public)"), funnel))
        } else {
            addrBox.addView(text("—", 14f))
        }
    }

    private fun renderCode() {
        if (!::adminCodeTv.isInitialized) return
        val json = if (Core.running) Mobile.loginCode() else ""
        if (json.isEmpty()) {
            adminCode = null
            userCode = null
            adminCodeTv.text = "— — —"
            userCodeTv.text = "— — —"
            codeBar.progress = 0
            codeInfoTv.text = L("Khởi động server để có mã đăng nhập.", "Start the server to get sign-in codes.")
        } else {
            val o = JSONObject(json)
            adminCode = o.getString("admin")
            userCode = o.getString("user")
            val step = o.optInt("step", 60)
            val left = ((o.getLong("expires") - System.currentTimeMillis() + 999) / 1000).toInt().coerceIn(0, step)
            adminCodeTv.text = adminCode
            userCodeTv.text = userCode
            codeBar.max = step
            codeBar.progress = left
            codeInfoTv.text = L("Đổi mã sau %d:%02d • mỗi mã dùng 1 lần", "New codes in %d:%02d • each code works once").format(left / 60, left % 60)
        }
        adminCopyBtn.isEnabled = adminCode != null
        userCopyBtn.isEnabled = userCode != null
    }

    private fun renderDevices() {
        if (!::devBox.isInitialized) return
        val json = if (Core.running) Mobile.devices() else "[]"
        if (json == lastDevicesJson) return
        lastDevicesJson = json
        devBox.removeAllViews()
        val arr = try { JSONArray(json) } catch (_: Exception) { JSONArray() }
        if (arr.length() == 0) {
            devBox.addView(text(if (Core.running) L("Chưa có thiết bị nào.", "No devices yet.") else "—", 14f))
            return
        }
        for (i in 0 until arr.length()) {
            val d = arr.getJSONObject(i)
            val id = d.getString("id")
            val name = d.getString("name")
            val seen = parseTime(d.optString("lastSeen"))
            val role = if (d.optString("role") == "admin") "ADMIN" else L("USER (chỉ xem)", "USER (view only)")
            val info = "$role • ${d.optString("via")} • ${d.optString("lastIP")} • " + L("lần cuối", "last seen") + " $seen • " +
                L("hết hạn", "expires") + " ${parseTime(d.optString("expires"))}"
            devBox.addView(hbox().apply {
                gravity = Gravity.CENTER_VERTICAL
                addView(text("$name\n$info", 13f), weighted())
                addView(button(L("Thu hồi", "Revoke")) {
                    thread { Mobile.revokeDevice(id); runOnUiThread { lastDevicesJson = ""; renderDevices() } }
                })
            })
        }
    }

    /** Danh sách app đã ghép đôi bằng cặp khóa, kèm đổi quyền và thu hồi. */
    private fun renderPaired() {
        if (!::pairedBox.isInitialized) return
        val json = if (Core.running) Mobile.pairedDevices() else "[]"
        if (json == lastPairedJson) return
        lastPairedJson = json
        pairedBox.removeAllViews()
        val arr = try { JSONArray(json) } catch (_: Exception) { JSONArray() }
        if (arr.length() == 0) {
            pairedBox.addView(text(if (Core.running) L("Chưa ghép ứng dụng nào.", "No apps paired yet.") else "—", 14f))
            return
        }
        for (i in 0 until arr.length()) {
            val d = arr.getJSONObject(i)
            val id = d.getString("id")
            val name = d.getString("name")
            val admin = d.optString("role") == "admin"
            val fp = d.optString("fp").take(16).uppercase().chunked(4).joinToString("-")
            val head = text((if (admin) "● ADMIN  " else "● USER  ") + name, 14f, bold = true).apply {
                setTextColor(Color.parseColor(if (admin) "#B42318" else "#2F6FED"))
            }
            val info = text(
                "${platformLabel(d.optString("platform"))} • " + L("khóa", "key") + " $fp\n" +
                    L("Lần cuối", "Last seen") + " ${parseTime(d.optString("lastSeen"))} " + L("qua", "via") + " ${d.optString("via")} • " +
                    L("ghép lúc", "paired") + " ${parseTime(d.optString("created"))}",
                12f
            )
            val actions = hbox()
            actions.addView(button(if (admin) L("Hạ xuống User", "Demote to User") else L("Nâng lên Admin", "Promote to Admin")) {
                val to = if (admin) "user" else "admin"
                AlertDialog.Builder(this)
                    .setTitle(L("Đổi quyền \"$name\"?", "Change access for \"$name\"?"))
                    .setMessage(if (admin) L("Thiết bị chỉ còn quyền xem và tải về.", "The device will only be able to view and download.") else L("Thiết bị sẽ có toàn quyền: tải lên, ghi đè, xóa.", "The device gets full access: upload, overwrite, delete."))
                    .setPositiveButton(L("Đổi", "Change")) { _, _ -> thread { Mobile.setPairedRole(id, to); runOnUiThread { lastPairedJson = ""; renderPaired() } } }
                    .setNegativeButton(L("Hủy", "Cancel"), null)
                    .show()
            }, weighted())
            actions.addView(button(L("Thu hồi", "Revoke")) {
                AlertDialog.Builder(this)
                    .setTitle(L("Thu hồi \"$name\"?", "Revoke \"$name\"?"))
                    .setMessage(L("Thiết bị bị ngắt ngay và không kết nối lại được. Muốn dùng lại phải ghép đôi lại.", "The device is disconnected now and cannot reconnect. Pair it again to use it."))
                    .setPositiveButton(L("Thu hồi", "Revoke")) { _, _ -> thread { Mobile.revokePaired(id); runOnUiThread { lastPairedJson = ""; renderPaired() } } }
                    .setNegativeButton(L("Hủy", "Cancel"), null)
                    .show()
            }, weighted())
            pairedBox.addView(vbox().apply {
                setPadding(0, dp(6), 0, dp(6))
                addView(head)
                addView(info)
                addView(actions)
            })
        }
    }

    private fun platformLabel(p: String) = when (p) {
        "windows" -> "Windows"
        "android" -> "Android"
        "ios" -> "iOS"
        "macos" -> "macOS"
        else -> p
    }

    /** Chọn quyền rồi hiện lời mời ghép đôi dạng QR + nút sao chép. */
    private fun newPairing() {
        if (!Core.running) { toast(L("Khởi động server trước", "Start the server first")); return }
        AlertDialog.Builder(this)
            .setTitle(L("Ghép thiết bị mới — chọn quyền", "Pair a new device — choose access"))
            .setItems(arrayOf(L("USER — chỉ xem & tải về", "USER — view & download only"), L("ADMIN — toàn quyền (tải lên, ghi đè, xóa)", "ADMIN — full access (upload, overwrite, delete)"))) { _, which ->
                showInvite(if (which == 1) "admin" else "user")
            }
            .setNegativeButton(L("Hủy", "Cancel"), null)
            .show()
    }

    private fun showInvite(role: String) {
        thread {
            val inv = try {
                JSONObject(Mobile.newPairInvite(role))
            } catch (e: Exception) {
                runOnUiThread { toast(e.message ?: L("Không tạo được lời mời", "Could not create an invite")) }
                return@thread
            }
            val invite = inv.getString("invite")
            val png = try { Mobile.qrpng(invite, 8) } catch (_: Exception) { null }
            runOnUiThread {
                val box = vbox().apply { setPadding(dp(20), dp(8), dp(20), 0); gravity = Gravity.CENTER_HORIZONTAL }
                if (png != null) {
                    val bmp = android.graphics.BitmapFactory.decodeByteArray(png, 0, png.size)
                    box.addView(android.widget.ImageView(this).apply {
                        setImageDrawable(android.graphics.drawable.BitmapDrawable(resources, bmp).apply {
                            isFilterBitmap = false // keep QR modules sharp when scaled
                        })
                        setBackgroundColor(Color.WHITE)
                    }, LinearLayout.LayoutParams(dp(260), dp(260)))
                }
                val roleText = if (role == "admin") L("ADMIN — toàn quyền", "ADMIN — full access") else L("USER — chỉ xem & tải về", "USER — view & download only")
                val expires = inv.getLong("expires")
                val info = text("", 13f).apply { gravity = Gravity.CENTER }
                box.addView(info)
                val dialog = AlertDialog.Builder(this)
                    .setTitle(L("Lời mời ghép đôi", "Pairing invite"))
                    .setView(box)
                    .setPositiveButton(L("Sao chép lời mời", "Copy invite")) { _, _ -> copy(invite) }
                    .setNegativeButton(L("Đóng", "Close"), null)
                    .show()
                val h = Handler(Looper.getMainLooper())
                val tickInfo = object : Runnable {
                    override fun run() {
                        val left = ((expires - System.currentTimeMillis()) / 1000).coerceAtLeast(0)
                        info.text = L("Quyền", "Access") + ": $roleText\n" + L("Vân tay server", "Server fingerprint") + ": ${inv.optString("fp")}\n" +
                            (if (left > 0) L("Dùng 1 lần • hết hạn sau %d:%02d", "Single use • expires in %d:%02d").format(left / 60, left % 60) else L("Đã hết hạn — tạo lời mời mới", "Expired — create a new invite")) +
                            L("\n\nQuét bằng app NASfone trên điện thoại, hoặc sao chép rồi dán vào app trên máy tính.", "\n\nScan with the NASfone app on a phone, or copy and paste it into the app on a computer.")
                        if (left > 0 && dialog.isShowing) h.postDelayed(this, 1000)
                    }
                }
                tickInfo.run()
            }
        }
    }

    private fun parseTime(iso: String): String = try {
        // Go ghi thời gian dạng RFC3339 có phần nano giây; chỉ cần đến giây.
        val base = iso.substring(0, 19)
        val tz = iso.substring(iso.indexOfAny(charArrayOf('Z', '+', '-'), 19))
        val fmt = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ssXXX", Locale.US)
        dateFmt.format(fmt.parse(base + tz) ?: Date())
    } catch (_: Exception) {
        "?"
    }

    // ---------------------------------------------------------------- actions

    private fun toggle() {
        if (Core.running) {
            stopService(Intent(this, NasService::class.java))
        } else {
            if (!save()) return
            startForegroundService(Intent(this, NasService::class.java))
        }
        render()
    }

    private fun save(): Boolean {
        val host = hostEt.text.toString().trim().lowercase()
        if (!host.matches(Regex("[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?"))) return toast(L("Tên máy chỉ gồm a-z, 0-9 và dấu -", "The machine name may only use a-z, 0-9 and -"))
        val control = controlEt.text.toString().trim()
        if (control.isNotEmpty() && !control.startsWith("https://")) return toast(L("Máy chủ điều khiển phải bắt đầu bằng https://", "The control server must start with https://"))

        if (control != prefs.controlUrl) {
            // Trạng thái đăng nhập gắn với máy chủ điều khiển cũ; xóa để đăng nhập lại.
            if (Core.running) return toast(L("Dừng server trước khi đổi máy chủ điều khiển", "Stop the server before changing the control server"))
            prefs.stateDir.deleteRecursively()
            Core.log(L("Đã đổi máy chủ điều khiển, cần đăng nhập lại", "Control server changed; sign in again"))
        }
        prefs.hostname = host
        prefs.controlUrl = control
        prefs.rootDir = rootEt.text.toString().trim()
        prefs.verboseLog = verboseCb.isChecked
        if (Core.running) toast(L("Đã lưu. Khởi động lại server để áp dụng.", "Saved. Restart the server to apply.")) else toast(L("Đã lưu", "Saved"))
        return true
    }

    private fun login() {
        val auth = Core.status.optString("authURL")
        if (auth.isNotEmpty()) {
            startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(auth)))
            return
        }
        thread {
            try { Mobile.login() } catch (e: Exception) { Core.log(L("Đăng nhập: ${e.message}", "Sign in: ${e.message}")) }
        }
        toast(L("Đang lấy link đăng nhập…", "Getting the sign-in link…"))
    }

    private fun logout() {
        AlertDialog.Builder(this)
            .setTitle(L("Đăng xuất Tailscale?", "Sign out of Tailscale?"))
            .setMessage(L("Server sẽ rời tailnet hiện tại. Sau đó bấm \"Đăng nhập\" để dùng tài khoản khác.", "The server leaves the current tailnet. Then tap \"Sign in\" to use another account."))
            .setPositiveButton(L("Đăng xuất", "Sign out")) { _, _ ->
                thread {
                    try { Mobile.logout() } catch (e: Exception) { Core.log(L("Đăng xuất: ${e.message}", "Sign out: ${e.message}")) }
                }
            }
            .setNegativeButton(L("Hủy", "Cancel"), null)
            .show()
    }

    private fun chooseLanguage() {
        val codes = arrayOf("", "en", "vi")
        AlertDialog.Builder(this)
            .setTitle(L("Ngôn ngữ", "Language"))
            .setItems(arrayOf(L("Theo ngôn ngữ của máy", "Follow the phone"), "English", "Tiếng Việt")) { _, which ->
                prefs.lang = codes[which]
                Lang.init(this)
                recreate() // rebuild the screen in the new language
            }
            .show()
    }

    private fun copy(s: String) {
        (getSystemService(CLIPBOARD_SERVICE) as ClipboardManager).setPrimaryClip(ClipData.newPlainText("NASfone", s))
        toast(L("Đã sao chép", "Copied"))
    }

    private fun toast(msg: String): Boolean {
        Toast.makeText(this, msg, Toast.LENGTH_SHORT).show()
        return false
    }
}
