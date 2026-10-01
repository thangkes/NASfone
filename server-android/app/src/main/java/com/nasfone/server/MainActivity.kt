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
                Core.log("Lỗi hiển thị: $e")
            }
            ticker.postDelayed(this, 1000)
        }
    }
    private val dateFmt = SimpleDateFormat("HH:mm dd/MM", Locale.US)

    private val onChange: () -> Unit = { render() }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        prefs = Prefs(this)
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

        section(col, "Mã đăng nhập web (6 số, đổi mỗi phút)")
        // Hai mã luôn khác nhau; màu và nhãn tách biệt để không đưa nhầm mã Admin.
        val (aTv, aBtn) = codeBlock(col, "ADMIN — toàn quyền (tải lên, ghi đè, xóa)", "#B42318") { adminCode }
        adminCodeTv = aTv; adminCopyBtn = aBtn
        val (uTv, uBtn) = codeBlock(col, "USER — chỉ xem & tải về", "#2F6FED") { userCode }
        userCodeTv = uTv; userCopyBtn = uBtn
        codeBar = ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal).apply { max = 60 }
            .also { col.addView(it, LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT)) }
        codeInfoTv = text("", 13f).apply { gravity = Gravity.CENTER }
            .also { col.addView(it, LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT)) }

        section(col, "Ứng dụng đã ghép (Windows / Android)")
        pairedBox = vbox().also { col.addView(it) }
        col.addView(button("＋ Ghép thiết bị mới") { newPairing() })

        section(col, "Phiên trình duyệt (đăng nhập bằng mã 6 số)")
        devBox = vbox().also { col.addView(it) }
        col.addView(button("Thu hồi tất cả") {
            AlertDialog.Builder(this)
                .setTitle("Thu hồi tất cả thiết bị?")
                .setMessage("Mọi trình duyệt đang đăng nhập sẽ bị đăng xuất ngay.")
                .setPositiveButton("Thu hồi") { _, _ -> thread { Mobile.revokeAllDevices(); runOnUiThread { lastDevicesJson = ""; renderDevices() } } }
                .setNegativeButton("Hủy", null)
                .show()
        })

        section(col, "Tài khoản Tailscale")
        accountBox = vbox().also { col.addView(it) }
        val accBtns = hbox().also { col.addView(it) }
        loginBtn = button("Đăng nhập") { login() }.also { accBtns.addView(it, weighted()) }
        logoutBtn = button("Đăng xuất / đổi tài khoản") { logout() }.also { accBtns.addView(it, weighted()) }

        section(col, "Địa chỉ truy cập")
        addrBox = vbox().also { col.addView(it) }

        section(col, "Funnel (truy cập công khai qua Internet)")
        funnelSw = Switch(this).apply {
            text = "Bật Funnel"
            isChecked = prefs.funnel
            setOnCheckedChangeListener { _, on ->
                prefs.funnel = on
                if (Core.running) thread { Mobile.setFunnel(on) }
            }
        }
        col.addView(funnelSw)
        funnelTv = text("", 13f).also { col.addView(it) }
        funnelHelpBtn = button("Mở trang cài đặt Tailscale để sửa") {
            Core.status.optString("funnelHelpURL").takeIf { it.isNotEmpty() }?.let {
                startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(it)))
            }
        }.also { col.addView(it) }

        section(col, "Cài đặt (áp dụng khi khởi động lại)")
        hostEt = field(col, "Tên máy trong tailnet", prefs.hostname)
        controlEt = field(col, "Máy chủ điều khiển (để trống = Tailscale; điền URL nếu dùng Headscale)", prefs.controlUrl)
        rootEt = field(col, "Thư mục lưu trữ", prefs.rootDir)
        verboseCb = CheckBox(this).apply { text = "Ghi log chi tiết của Tailscale"; isChecked = prefs.verboseLog }.also { col.addView(it) }
        autoCb = CheckBox(this).apply {
            text = "Tự chạy khi khởi động máy"
            isChecked = prefs.autoStart
            setOnCheckedChangeListener { _, on -> prefs.autoStart = on }
        }.also { col.addView(it) }
        col.addView(button("Lưu cài đặt") { save() })

        section(col, "Quyền & chống bị tắt nền (MagicOS)")
        permTv = text("", 14f).also { col.addView(it) }
        col.addView(button("Cấp quyền truy cập tất cả file") {
            startActivity(Intent(Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION, Uri.parse("package:$packageName")))
        })
        col.addView(button("Bỏ tối ưu pin") {
            @Suppress("BatteryLife")
            startActivity(Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:$packageName")))
        })
        col.addView(button("Mở cài đặt ứng dụng (Khởi chạy ứng dụng → thủ công)") {
            startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:$packageName")))
        })

        section(col, "Nhật ký")
        logTv = text("", 11f).apply { typeface = Typeface.MONOSPACE; setTextIsSelectable(true) }.also { col.addView(it) }
        col.addView(button("Sao chép nhật ký") { copy(Core.logLines().joinToString("\n")) })
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
        val btn = button("Sao chép") { current()?.let { copy(it.replace(" ", "")) } }.also { row.addView(it) }
        return tv to btn
    }

    /** Một dòng "nhãn: giá trị" kèm nút sao chép. */
    private fun row(label: String, value: String) = hbox().apply {
        gravity = Gravity.CENTER_VERTICAL
        addView(text("$label\n$value", 14f).apply { setTextIsSelectable(true) }, weighted())
        addView(button("Sao chép") { copy(value) })
    }

    // ---------------------------------------------------------------- render

    private fun render() {
        val st = Core.status
        val backend = st.optString("backendState")
        val running = Core.running

        stateTv.text = when {
            !running && Core.startError != null -> "⛔ Lỗi: ${Core.startError}"
            !running -> "○ Đã dừng"
            backend == "Running" -> "● Đang chạy • tailnet đã kết nối"
            backend == "NeedsLogin" -> "● Đang chạy • chờ đăng nhập Tailscale"
            backend.isEmpty() -> "● Đang khởi động…"
            else -> "● Đang chạy • $backend"
        }
        stateTv.setTextColor(Color.parseColor(if (running) "#1F9D55" else "#D64545"))
        toggleBtn.text = if (running) "Dừng server" else "Khởi động server"

        accountBox.removeAllViews()
        if (running) {
            val login = st.optString("loginName")
            val tailnet = st.optString("tailnetName")
            accountBox.addView(text(
                when {
                    login.isNotEmpty() -> "Tài khoản: $login\nTailnet: $tailnet"
                    backend == "NeedsLogin" -> "Chưa đăng nhập. Bấm \"Đăng nhập\" để mở trang đăng nhập Tailscale."
                    else -> "…"
                }, 14f
            ))
            val auth = st.optString("authURL")
            if (auth.isNotEmpty()) accountBox.addView(row("Link đăng nhập (mở trên máy này hoặc máy khác)", auth))
        } else {
            accountBox.addView(text("Khởi động server để đăng nhập.", 14f))
        }
        loginBtn.isEnabled = running && backend != "Running"
        logoutBtn.isEnabled = running && backend == "Running"

        addrBox.removeAllViews()
        if (running) {
            val dns = st.optString("dnsName")
            if (dns.isNotEmpty()) addrBox.addView(row("Tailnet", "http://$dns"))
            val ips = st.optJSONArray("tailscaleIPs")
            if (ips != null && ips.length() > 0) addrBox.addView(row("Tailnet IP", "http://${ips.getString(0)}"))
            val funnel = st.optString("funnelURL")
            if (funnel.isNotEmpty()) addrBox.addView(row("Funnel (công khai)", funnel))
        } else {
            addrBox.addView(text("—", 14f))
        }

        funnelTv.text = when {
            st.optString("funnelError").isNotEmpty() ->
                "⚠ ${st.optString("funnelError")}\nApp tự thử lại mỗi 20 giây sau khi bạn sửa."
            st.optString("funnelURL").isNotEmpty() -> "Đang mở công khai: ${st.optString("funnelURL")}"
            prefs.funnel -> "Sẽ mở khi tailnet kết nối xong."
            else -> "Tắt — chỉ truy cập qua LAN và tailnet."
        }
        funnelHelpBtn.visibility = if (st.optString("funnelHelpURL").isNotEmpty()) View.VISIBLE else View.GONE

        val files = Environment.isExternalStorageManager()
        val battery = (getSystemService(POWER_SERVICE) as PowerManager).isIgnoringBatteryOptimizations(packageName)
        permTv.text = "${if (files) "✔" else "✘"} Truy cập tất cả file\n${if (battery) "✔" else "✘"} Bỏ tối ưu pin"

        logTv.text = Core.logLines().takeLast(80).joinToString("\n")
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
            codeInfoTv.text = "Khởi động server để có mã đăng nhập."
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
            codeInfoTv.text = "Đổi mã sau %d:%02d • mỗi mã dùng 1 lần".format(left / 60, left % 60)
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
            devBox.addView(text(if (Core.running) "Chưa có thiết bị nào." else "—", 14f))
            return
        }
        for (i in 0 until arr.length()) {
            val d = arr.getJSONObject(i)
            val id = d.getString("id")
            val name = d.getString("name")
            val seen = parseTime(d.optString("lastSeen"))
            val role = if (d.optString("role") == "admin") "ADMIN" else "USER (chỉ xem)"
            val info = "$role • ${d.optString("via")} • ${d.optString("lastIP")} • lần cuối $seen • hết hạn ${parseTime(d.optString("expires"))}"
            devBox.addView(hbox().apply {
                gravity = Gravity.CENTER_VERTICAL
                addView(text("$name\n$info", 13f), weighted())
                addView(button("Thu hồi") {
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
            pairedBox.addView(text(if (Core.running) "Chưa ghép ứng dụng nào." else "—", 14f))
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
                "${platformLabel(d.optString("platform"))} • khóa $fp\n" +
                    "Lần cuối ${parseTime(d.optString("lastSeen"))} qua ${d.optString("via")} • ghép lúc ${parseTime(d.optString("created"))}",
                12f
            )
            val actions = hbox()
            actions.addView(button(if (admin) "Hạ xuống User" else "Nâng lên Admin") {
                val to = if (admin) "user" else "admin"
                AlertDialog.Builder(this)
                    .setTitle("Đổi quyền \"$name\"?")
                    .setMessage(if (admin) "Thiết bị chỉ còn quyền xem và tải về." else "Thiết bị sẽ có toàn quyền: tải lên, ghi đè, xóa.")
                    .setPositiveButton("Đổi") { _, _ -> thread { Mobile.setPairedRole(id, to); runOnUiThread { lastPairedJson = ""; renderPaired() } } }
                    .setNegativeButton("Hủy", null)
                    .show()
            }, weighted())
            actions.addView(button("Thu hồi") {
                AlertDialog.Builder(this)
                    .setTitle("Thu hồi \"$name\"?")
                    .setMessage("Thiết bị bị ngắt ngay và không kết nối lại được. Muốn dùng lại phải ghép đôi lại.")
                    .setPositiveButton("Thu hồi") { _, _ -> thread { Mobile.revokePaired(id); runOnUiThread { lastPairedJson = ""; renderPaired() } } }
                    .setNegativeButton("Hủy", null)
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
        if (!Core.running) { toast("Khởi động server trước"); return }
        AlertDialog.Builder(this)
            .setTitle("Ghép thiết bị mới — chọn quyền")
            .setItems(arrayOf("USER — chỉ xem & tải về", "ADMIN — toàn quyền (tải lên, ghi đè, xóa)")) { _, which ->
                showInvite(if (which == 1) "admin" else "user")
            }
            .setNegativeButton("Hủy", null)
            .show()
    }

    private fun showInvite(role: String) {
        thread {
            val inv = try {
                JSONObject(Mobile.newPairInvite(role))
            } catch (e: Exception) {
                runOnUiThread { toast(e.message ?: "Không tạo được lời mời") }
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
                val roleText = if (role == "admin") "ADMIN — toàn quyền" else "USER — chỉ xem & tải về"
                val expires = inv.getLong("expires")
                val info = text("", 13f).apply { gravity = Gravity.CENTER }
                box.addView(info)
                val dialog = AlertDialog.Builder(this)
                    .setTitle("Lời mời ghép đôi")
                    .setView(box)
                    .setPositiveButton("Sao chép lời mời") { _, _ -> copy(invite) }
                    .setNegativeButton("Đóng", null)
                    .show()
                val h = Handler(Looper.getMainLooper())
                val tickInfo = object : Runnable {
                    override fun run() {
                        val left = ((expires - System.currentTimeMillis()) / 1000).coerceAtLeast(0)
                        info.text = "Quyền: $roleText\nVân tay server: ${inv.optString("fp")}\n" +
                            (if (left > 0) "Dùng 1 lần • hết hạn sau %d:%02d".format(left / 60, left % 60) else "Đã hết hạn — tạo lời mời mới") +
                            "\n\nQuét bằng app NASfone trên điện thoại, hoặc sao chép rồi dán vào app trên máy tính."
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
        if (!host.matches(Regex("[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?"))) return toast("Tên máy chỉ gồm a-z, 0-9 và dấu -")
        val control = controlEt.text.toString().trim()
        if (control.isNotEmpty() && !control.startsWith("https://")) return toast("Máy chủ điều khiển phải bắt đầu bằng https://")

        if (control != prefs.controlUrl) {
            // Trạng thái đăng nhập gắn với máy chủ điều khiển cũ; xóa để đăng nhập lại.
            if (Core.running) return toast("Dừng server trước khi đổi máy chủ điều khiển")
            prefs.stateDir.deleteRecursively()
            Core.log("Đã đổi máy chủ điều khiển, cần đăng nhập lại")
        }
        prefs.hostname = host
        prefs.controlUrl = control
        prefs.rootDir = rootEt.text.toString().trim()
        prefs.verboseLog = verboseCb.isChecked
        if (Core.running) toast("Đã lưu. Khởi động lại server để áp dụng.") else toast("Đã lưu")
        return true
    }

    private fun login() {
        val auth = Core.status.optString("authURL")
        if (auth.isNotEmpty()) {
            startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(auth)))
            return
        }
        thread {
            try { Mobile.login() } catch (e: Exception) { Core.log("Đăng nhập: ${e.message}") }
        }
        toast("Đang lấy link đăng nhập…")
    }

    private fun logout() {
        AlertDialog.Builder(this)
            .setTitle("Đăng xuất Tailscale?")
            .setMessage("Server sẽ rời tailnet hiện tại. Sau đó bấm \"Đăng nhập\" để dùng tài khoản khác.")
            .setPositiveButton("Đăng xuất") { _, _ ->
                thread {
                    try { Mobile.logout() } catch (e: Exception) { Core.log("Đăng xuất: ${e.message}") }
                }
            }
            .setNegativeButton("Hủy", null)
            .show()
    }

    private fun copy(s: String) {
        (getSystemService(CLIPBOARD_SERVICE) as ClipboardManager).setPrimaryClip(ClipData.newPlainText("NASfone", s))
        toast("Đã sao chép")
    }

    private fun toast(msg: String): Boolean {
        Toast.makeText(this, msg, Toast.LENGTH_SHORT).show()
        return false
    }
}
