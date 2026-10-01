package com.pocketnas.server

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.graphics.drawable.Icon
import android.net.wifi.WifiManager
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import com.pocketnas.core.mobile.Mobile
import org.json.JSONObject
import java.io.File
import kotlin.concurrent.thread

class NasService : Service() {
    private var wakeLock: PowerManager.WakeLock? = null
    private var wifiLock: WifiManager.WifiLock? = null
    private var lastNotifText = ""
    private val onChange: () -> Unit = { updateNotification() }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopSelf()
            return START_NOT_STICKY
        }
        Core.logFile = getExternalFilesDir(null)?.let { File(it, "pnas.log") }
        Core.appContext = applicationContext
        startInForeground("Đang khởi động…")
        if (!Core.running) {
            Core.running = true
            Core.startError = null
            acquireLocks()
            Core.addListener(onChange)
            val prefs = Prefs(this)
            val cfg = JSONObject()
                .put("stateDir", prefs.stateDir.path)
                .put("authFile", File(filesDir, "auth.json").path)
                .put("rootDir", prefs.rootDir)
                .put("hostname", prefs.hostname)
                .put("controlURL", prefs.controlUrl)
                .put("lanPort", prefs.lanPort)
                .put("password", prefs.password)
                .put("funnel", prefs.funnel)
                .put("verbose", prefs.verboseLog)
            thread(name = "pnas-start") {
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
        if (Core.running) {
            Core.running = false
            // ts.Close() có thể mất vài giây; không chặn luồng chính.
            thread(name = "pnas-stop") { Mobile.stop(); Core.notifyChanged() }
        }
        wakeLock?.let { if (it.isHeld) it.release() }
        wifiLock?.let { if (it.isHeld) it.release() }
        super.onDestroy()
    }

    private fun acquireLocks() {
        val pm = getSystemService(Context.POWER_SERVICE) as PowerManager
        wakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "PocketNAS:server").apply { acquire() }
        val wm = applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
        @Suppress("DEPRECATION")
        wifiLock = wm.createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "PocketNAS:wifi").apply { acquire() }
    }

    private fun updateNotification() {
        if (!Core.running) return
        val st = Core.status
        val text = when (st.optString("backendState")) {
            "Running" -> st.optString("dnsName").ifEmpty { "Tailnet đã kết nối" } +
                if (st.optString("funnelURL").isNotEmpty()) " • Funnel bật" else ""
            "NeedsLogin" -> "Chờ đăng nhập Tailscale • LAN cổng ${st.optInt("lanPort")}"
            "" -> "Đang khởi động…"
            else -> st.optString("backendState") + " • LAN cổng ${st.optInt("lanPort")}"
        }
        if (text != lastNotifText) startInForeground(text)
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
            .setContentTitle("PocketNAS đang chạy")
            .setContentText(text)
            .setOngoing(true)
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
        const val ACTION_STOP = "com.pocketnas.server.STOP"
        private const val CHANNEL = "server"
        private const val NOTIF_ID = 1
    }
}
