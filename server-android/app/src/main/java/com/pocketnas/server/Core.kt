package com.pocketnas.server

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.Handler
import android.os.Looper
import android.util.Log
import com.pocketnas.core.mobile.Host
import org.json.JSONObject
import java.net.Inet4Address
import java.net.NetworkInterface
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.concurrent.CopyOnWriteArrayList

/** Cầu nối giữa lõi Go và phần Android. Các callback từ Go chạy trên luồng của Go. */
object Core : Host {
    @Volatile var running = false
    @Volatile var startError: String? = null
    @Volatile var status: JSONObject = JSONObject()
        private set

    private val logs = ArrayDeque<String>()
    private val listeners = CopyOnWriteArrayList<() -> Unit>()
    private val main = Handler(Looper.getMainLooper())
    private val timeFmt = SimpleDateFormat("HH:mm:ss", Locale.US)

    /** Context ứng dụng, đặt khi service khởi động; dùng để hiện thông báo bảo mật. */
    @Volatile var appContext: Context? = null

    /** Bản sao nhật ký ra file (đọc được qua adb), vì logcat trên MagicOS bị mã hóa. */
    @Volatile var logFile: java.io.File? = null

    fun addListener(l: () -> Unit) = listeners.add(l)
    fun removeListener(l: () -> Unit) = listeners.remove(l)
    fun notifyChanged() = main.post { listeners.forEach { it() } }

    fun logLines(): List<String> = synchronized(logs) { logs.toList() }

    fun log(line: String) {
        Log.i("PocketNAS", line)
        synchronized(logs) {
            logs.addLast(timeFmt.format(Date()) + "  " + line)
            while (logs.size > 300) logs.removeFirst()
            logFile?.let { f ->
                try {
                    if (f.length() > 2_000_000) f.delete()
                    f.appendText(timeFmt.format(Date()) + "  " + line + "\n")
                } catch (_: Exception) {
                }
            }
        }
        notifyChanged()
    }

    override fun onStatus(statusJSON: String) {
        status = try { JSONObject(statusJSON) } catch (_: Exception) { JSONObject() }
        notifyChanged()
    }

    override fun onLog(line: String) = log(line)

    override fun onEvent(kind: String, detail: String) {
        val text = when (kind) {
            "login" -> "Thiết bị mới đăng nhập: $detail"
            "revoke" -> "Đã thu hồi: $detail"
            "code_rolled" -> "Mã đăng nhập bị nhập sai 5 lần (IP $detail), đã đổi mã mới"
            else -> "$kind: $detail"
        }
        log(text)
        if (kind == "login" || kind == "code_rolled") notifySecurity(text)
    }

    private fun notifySecurity(text: String) {
        val ctx = appContext ?: return
        val nm = ctx.getSystemService(NotificationManager::class.java)
        if (nm.getNotificationChannel("security") == null) {
            nm.createNotificationChannel(NotificationChannel("security", "Bảo mật", NotificationManager.IMPORTANCE_HIGH))
        }
        val open = PendingIntent.getActivity(ctx, 2, Intent(ctx, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        nm.notify(
            (System.currentTimeMillis() % 100000).toInt() + 10,
            Notification.Builder(ctx, "security")
                .setSmallIcon(R.drawable.ic_stat)
                .setContentTitle("PocketNAS")
                .setContentText(text)
                .setContentIntent(open)
                .setAutoCancel(true)
                .build()
        )
    }

    /** Android 11+ không cho Go đọc danh sách card mạng, nên Kotlin cung cấp thay. */
    override fun interfaces(): String {
        val sb = StringBuilder()
        try {
            for (ni in NetworkInterface.getNetworkInterfaces()) {
                val flags = StringBuilder()
                if (ni.isUp) flags.append('u')
                if (ni.isLoopback) flags.append('l')
                if (ni.isPointToPoint) flags.append('p')
                if (ni.supportsMulticast()) flags.append('m')
                val addrs = ni.interfaceAddresses.mapNotNull { ia ->
                    if (ia.broadcast != null && 'b' !in flags) flags.append('b')
                    val host = ia.address?.hostAddress?.substringBefore('%') ?: return@mapNotNull null
                    "$host/${ia.networkPrefixLength}"
                }
                sb.append(ni.name).append('|').append(ni.index).append('|').append(ni.mtu).append('|')
                    .append(flags).append('|').append(addrs.joinToString(",")).append('\n')
            }
        } catch (e: Exception) {
            Log.w("PocketNAS", "interfaces: $e")
        }
        return sb.toString()
    }

    /** Địa chỉ IPv4 trong mạng nội bộ (Wi-Fi, hotspot, USB tethering), kèm nhãn. */
    fun lanAddresses(): List<Pair<String, String>> {
        val out = mutableListOf<Pair<String, String>>()
        try {
            for (ni in NetworkInterface.getNetworkInterfaces()) {
                if (!ni.isUp || ni.isLoopback) continue
                val n = ni.name
                if (listOf("rmnet", "ccmni", "dummy", "tun", "ifb", "v4-").any { n.startsWith(it) }) continue
                for (a in ni.inetAddresses) {
                    if (a is Inet4Address && !a.isLoopbackAddress) out += label(n) to a.hostAddress!!
                }
            }
        } catch (_: Exception) {
        }
        return out
    }

    private fun label(name: String) = when {
        name == "wlan0" -> "Wi-Fi"
        name.startsWith("ap") || name.startsWith("swlan") || name.startsWith("wlan") -> "Hotspot ($name)"
        name.startsWith("rndis") || name.startsWith("usb") || name.startsWith("ncm") -> "USB tethering"
        name.startsWith("eth") -> "Ethernet"
        else -> name
    }
}
