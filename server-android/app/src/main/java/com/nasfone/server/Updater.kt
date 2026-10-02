package com.nasfone.server

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInstaller
import android.os.Build
import com.nasfone.core.mobile.Mobile
import org.json.JSONObject
import java.io.File
import kotlin.concurrent.thread

/**
 * Tự cập nhật từ GitHub Releases. Lõi Go tìm bản mới và tải APK (kiểm SHA-256);
 * phần này chỉ báo cho người dùng và giao APK cho trình cài đặt của Android.
 * Android không cho app ngoài Play Store cài ngầm: người dùng luôn bấm "Cài đặt".
 */
object Updater {
    private const val CHANNEL = "updates"
    private const val NOTIF_ID = 2
    private const val CHECK_EVERY_MS = 6 * 60 * 60 * 1000L

    @Volatile var available: JSONObject? = null
        private set
    @Volatile var busy = false
        private set
    @Volatile private var checking = false

    fun currentVersion(ctx: Context): String =
        ctx.packageManager.getPackageInfo(ctx.packageName, 0).versionName ?: "dev"

    /** Kiểm tra nếu lần trước đã quá lâu (hoặc force). onDone chạy trên luồng nền. */
    fun maybeCheck(ctx: Context, force: Boolean = false, onDone: (JSONObject?, Exception?) -> Unit = { _, _ -> }) {
        val app = ctx.applicationContext
        val sp = app.getSharedPreferences("nasfone", Context.MODE_PRIVATE)
        val now = System.currentTimeMillis()
        if (!force && now - sp.getLong("updateCheckedAt", 0) < CHECK_EVERY_MS) {
            onDone(available, null)
            return
        }
        if (checking) return
        checking = true
        thread(name = "update-check") {
            try {
                val js = Mobile.checkUpdate(currentVersion(app))
                sp.edit().putLong("updateCheckedAt", now).apply()
                val rel = if (js.isNullOrEmpty()) null else JSONObject(js)
                available = rel
                if (rel != null) notifyAvailable(app, rel, sp)
                Core.notifyChanged()
                onDone(rel, null)
            } catch (e: Exception) {
                onDone(null, e)
            } finally {
                checking = false
            }
        }
    }

    private fun notifyAvailable(ctx: Context, rel: JSONObject, sp: android.content.SharedPreferences) {
        val ver = rel.getString("version")
        if (sp.getString("updateNotified", "") == ver) return // báo mỗi phiên bản một lần
        sp.edit().putString("updateNotified", ver).apply()
        val nm = ctx.getSystemService(NotificationManager::class.java)
        if (nm.getNotificationChannel(CHANNEL) == null) {
            nm.createNotificationChannel(NotificationChannel(CHANNEL, L("Cập nhật", "Updates"), NotificationManager.IMPORTANCE_DEFAULT))
        }
        val open = PendingIntent.getActivity(ctx, 0, Intent(ctx, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        val n = Notification.Builder(ctx, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat)
            .setContentTitle(L("Đã có NASfone v$ver", "NASfone v$ver is available"))
            .setContentText(L("Mở app để cập nhật", "Open the app to update"))
            .setContentIntent(open)
            .setAutoCancel(true)
            .build()
        nm.notify(NOTIF_ID, n)
    }

    /**
     * Tải APK rồi mở hộp thoại cài đặt của hệ thống. onError chạy trên luồng nền.
     * Gọi khi app đang ở tiền cảnh (người dùng vừa bấm nút).
     */
    fun install(ctx: Context, onError: (String) -> Unit) {
        val rel = available ?: return
        if (busy) return
        busy = true
        Core.notifyChanged()
        val app = ctx.applicationContext
        thread(name = "update-install") {
            try {
                val dir = File(app.cacheDir, "update").apply { mkdirs() }
                dir.listFiles()?.forEach { it.delete() }
                val apk = File(dir, rel.getString("assetName"))
                Mobile.downloadUpdate(rel.toString(), apk.path)
                val pi = app.packageManager.packageInstaller
                val params = PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL)
                if (Build.VERSION.SDK_INT >= 31) {
                    params.setRequireUserAction(PackageInstaller.SessionParams.USER_ACTION_REQUIRED)
                }
                val id = pi.createSession(params)
                pi.openSession(id).use { s ->
                    s.openWrite("nasfone.apk", 0, apk.length()).use { out ->
                        apk.inputStream().use { it.copyTo(out) }
                        s.fsync(out)
                    }
                    val cb = PendingIntent.getBroadcast(
                        app, id, Intent(app, InstallResultReceiver::class.java),
                        PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_MUTABLE
                    )
                    s.commit(cb.intentSender)
                }
                Core.log(L("Đã tải v${rel.getString("version")}, đang mở trình cài đặt", "Downloaded v${rel.getString("version")}, opening the installer"))
            } catch (e: Exception) {
                onError(e.message ?: e.toString())
            } finally {
                busy = false
                Core.notifyChanged()
            }
        }
    }
}

/** Nhận kết quả từ PackageInstaller: mở màn hình xác nhận, hoặc ghi lỗi. */
class InstallResultReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        Lang.init(context)
        when (val status = intent.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE)) {
            PackageInstaller.STATUS_PENDING_USER_ACTION -> {
                @Suppress("DEPRECATION")
                val confirm = intent.getParcelableExtra<Intent>(Intent.EXTRA_INTENT) ?: return
                context.startActivity(confirm.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            }
            PackageInstaller.STATUS_SUCCESS -> Unit // app sẽ được khởi động lại (MY_PACKAGE_REPLACED)
            else -> Core.log(L("Cập nhật thất bại ($status): ", "Update failed ($status): ") +
                (intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE) ?: ""))
        }
    }
}
