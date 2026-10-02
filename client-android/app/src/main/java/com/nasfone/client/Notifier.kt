package com.nasfone.client

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent

/** Notifications: backup progress and available updates. */
object Notifier {
    const val CH_BACKUP = "backup"
    const val CH_UPDATE = "updates"
    private const val ID_BACKUP = 1
    const val ID_UPDATE = 2

    fun channels(ctx: Context) {
        val nm = ctx.getSystemService(NotificationManager::class.java)
        if (nm.getNotificationChannel(CH_BACKUP) == null) {
            nm.createNotificationChannel(NotificationChannel(CH_BACKUP, L("Sao lưu ảnh", "Photo backup"), NotificationManager.IMPORTANCE_LOW))
        }
        if (nm.getNotificationChannel(CH_UPDATE) == null) {
            nm.createNotificationChannel(NotificationChannel(CH_UPDATE, L("Cập nhật", "Updates"), NotificationManager.IMPORTANCE_DEFAULT))
        }
    }

    fun openApp(ctx: Context): PendingIntent =
        PendingIntent.getActivity(ctx, 0, Intent(ctx, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)

    fun progress(ctx: Context, done: Int, total: Int, host: String) {
        channels(ctx)
        val n = Notification.Builder(ctx, CH_BACKUP)
            .setSmallIcon(R.drawable.ic_stat)
            .setContentTitle(L("Đang sao lưu ảnh & video", "Backing up photos & videos"))
            .setContentText("$done / $total → $host")
            .setProgress(total, done, false)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setContentIntent(openApp(ctx))
            .build()
        try {
            ctx.getSystemService(NotificationManager::class.java).notify(ID_BACKUP, n)
        } catch (_: SecurityException) {
        }
    }

    fun done(ctx: Context) {
        ctx.getSystemService(NotificationManager::class.java).cancel(ID_BACKUP)
    }
}
