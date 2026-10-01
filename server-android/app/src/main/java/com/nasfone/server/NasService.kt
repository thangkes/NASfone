package com.nasfone.server

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.graphics.drawable.Icon
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.os.IBinder
import android.os.PowerManager
import com.nasfone.core.mobile.Mobile
import org.json.JSONObject
import java.io.File
import kotlin.concurrent.thread

class NasService : Service() {
    private var wakeLock: PowerManager.WakeLock? = null
    private var wifiLock: WifiManager.WifiLock? = null
    private var lastNotifText = ""
    private val onChange: () -> Unit = { updateNotification() }

    // Đo tốc độ từ chênh lệch bộ đếm byte giữa hai lần cập nhật trạng thái.
    private var lastIn = -1L
    private var lastOut = -1L
    private var lastAt = 0L
    private var speedIn = 0.0
    private var speedOut = 0.0

    private val main = Handler(Looper.getMainLooper())
    private var netCallback: ConnectivityManager.NetworkCallback? = null
    private var lastNetwork: Network? = null
    private val nudge = Runnable { thread(name = "nasfone-netchange") { Mobile.networkChanged() } }
    // Cập nhật định kỳ để tốc độ về 0 khi hết truyền (trạng thái khi đó không đổi nên không có sự kiện).
    private val tick = object : Runnable {
        override fun run() {
            updateNotification()
            main.postDelayed(this, 2000)
        }
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopSelf()
            return START_NOT_STICKY
        }
        Core.logFile = getExternalFilesDir(null)?.let { File(it, "nasfone.log") }
        Core.appContext = applicationContext
        startInForeground("Đang khởi động…")
        if (!Core.running) {
            Core.running = true
            Core.startError = null
            acquireLocks()
            watchNetwork()
            Core.addListener(onChange)
            main.postDelayed(tick, 2000)
            val prefs = Prefs(this)
            val cfg = JSONObject()
                .put("stateDir", prefs.stateDir.path)
                .put("authFile", File(filesDir, "auth.json").path)
                .put("rootDir", prefs.rootDir)
                .put("hostname", prefs.hostname)
                .put("controlURL", prefs.controlUrl)
                .put("funnel", prefs.funnel)
                .put("verbose", prefs.verboseLog)
            thread(name = "nasfone-start") {
                try {
                    if (!File(prefs.rootDir).let { it.isDirectory || it.mkdirs() }) {
                        throw IllegalStateException("Không truy cập được ${prefs.rootDir}. Đã cấp quyền \"Truy cập tất cả file\" chưa?")
                    }
                    getExternalFilesDir(null)?.let { Mobile.setCrashFile(File(it, "go-crash.txt").path) }
                    Mobile.start(cfg.toString(), Core)
                } catch (e: Exception) {
                    Core.startError = e.message ?: e.toString()
                    Core.log("Lỗi khởi động: ${Core.startError}")
                    Core.running = false
                    Core.notifyChanged()
                    stopSelf()
                }
            }
        }
        return START_STICKY
    }

    override fun onDestroy() {
        Core.removeListener(onChange)
        netCallback?.let { getSystemService(ConnectivityManager::class.java).unregisterNetworkCallback(it) }
        netCallback = null
        main.removeCallbacks(nudge)
        main.removeCallbacks(tick)
        Core.networkType = null
        if (Core.running) {
            Core.running = false
            // ts.Close() có thể mất vài giây; không chặn luồng chính.
            thread(name = "nasfone-stop") { Mobile.stop(); Core.notifyChanged() }
        }
        wakeLock?.let { if (it.isHeld) it.release() }
        wifiLock?.let { if (it.isHeld) it.release() }
        super.onDestroy()
    }

    private fun acquireLocks() {
        val pm = getSystemService(Context.POWER_SERVICE) as PowerManager
        wakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "NASfone:server").apply { acquire() }
        val wm = applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
        @Suppress("DEPRECATION")
        wifiLock = wm.createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "NASfone:wifi").apply { acquire() }
    }

    /**
     * Theo dõi mạng mặc định (Wi-Fi, 4G…). Khi đổi mạng thì báo lõi Go nối lại
     * Tailscale ngay, thay vì đợi các kết nối cũ tự hết hạn.
     */
    private fun watchNetwork() {
        val cm = getSystemService(ConnectivityManager::class.java)
        val cb = object : ConnectivityManager.NetworkCallback() {
            override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) {
                val type = when {
                    caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> "Wi-Fi"
                    caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> "4G/5G"
                    caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> "Ethernet"
                    else -> "mạng khác"
                }
                val changed = network != lastNetwork
                lastNetwork = network
                if (Core.networkType != type || changed) {
                    val first = Core.networkType == null
                    Core.networkType = type
                    if (!first) {
                        Core.log("Đã chuyển sang $type")
                        // Gom các sự kiện dồn dập khi đổi mạng thành một lần nối lại.
                        main.removeCallbacks(nudge)
                        main.postDelayed(nudge, 1500)
                    }
                    Core.notifyChanged()
                }
            }

            override fun onLost(network: Network) {
                if (network == lastNetwork) {
                    lastNetwork = null
                    Core.networkType = ""
                    Core.log("Mất kết nối mạng")
                    Core.notifyChanged()
                }
            }
        }
        cm.registerDefaultNetworkCallback(cb)
        netCallback = cb
    }

    private fun updateNotification() {
        if (!Core.running) return
        val st = Core.status

        val now = System.currentTimeMillis()
        val bin = st.optLong("bytesIn", 0)
        val bout = st.optLong("bytesOut", 0)
        if (lastIn >= 0 && now > lastAt) {
            val secs = (now - lastAt) / 1000.0
            if (secs >= 0.5) {
                speedIn = (bin - lastIn).coerceAtLeast(0) / secs
                speedOut = (bout - lastOut).coerceAtLeast(0) / secs
                lastIn = bin; lastOut = bout; lastAt = now
            }
        } else {
            lastIn = bin; lastOut = bout; lastAt = now
        }

        val parts = mutableListOf<String>()
        val net = Core.networkType
        when {
            net == "" -> parts += "⚠ Mất mạng, đang chờ kết nối lại"
            st.optString("backendState") == "Running" -> parts += "Tailnet ✓" + (net?.let { " ($it)" } ?: "")
            st.optString("backendState") == "NeedsLogin" -> parts += "Chờ đăng nhập Tailscale"
            st.optString("backendState").isEmpty() -> parts += "Đang khởi động…"
            else -> parts += st.optString("backendState")
        }
        if (st.optString("funnelURL").isNotEmpty()) parts += "Funnel"
        val conns = st.optInt("openConns")
        if (conns > 0) parts += "$conns kết nối"
        // ↓ = người dùng đang tải về (server gửi đi), ↑ = đang tải lên.
        if (speedOut >= 1024 || speedIn >= 1024) parts += "↓${rate(speedOut)} ↑${rate(speedIn)}"
        val sessions = st.optInt("sessions")
        if (sessions > 0) parts += "$sessions thiết bị"

        val text = parts.joinToString(" • ")
        if (text != lastNotifText) startInForeground(text)
    }

    private fun rate(bps: Double): String = when {
        bps >= 1024 * 1024 -> "%.1f MB/s".format(bps / 1024 / 1024)
        bps >= 1024 -> "%.0f KB/s".format(bps / 1024)
        else -> "0"
    }

    private fun startInForeground(text: String) {
        lastNotifText = text
        val nm = getSystemService(NotificationManager::class.java)
        if (nm.getNotificationChannel(CHANNEL) == null) {
            nm.createNotificationChannel(NotificationChannel(CHANNEL, "Server", NotificationManager.IMPORTANCE_LOW))
        }
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE
        )
        val stop = PendingIntent.getService(
            this, 1, Intent(this, NasService::class.java).setAction(ACTION_STOP), PendingIntent.FLAG_IMMUTABLE
        )
        val n = Notification.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat)
            .setContentTitle("NASfone đang chạy")
            .setContentText(text)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setContentIntent(open)
            .addAction(Notification.Action.Builder(Icon.createWithResource(this, R.drawable.ic_stat), "Dừng", stop).build())
            .build()
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(NOTIF_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(NOTIF_ID, n)
        }
    }

    companion object {
        const val ACTION_STOP = "com.nasfone.server.STOP"
        private const val CHANNEL = "server"
        private const val NOTIF_ID = 1
    }
}
