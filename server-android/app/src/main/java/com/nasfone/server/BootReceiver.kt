package com.nasfone.server

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val ok = intent.action == Intent.ACTION_BOOT_COMPLETED || intent.action == Intent.ACTION_MY_PACKAGE_REPLACED
        if (!ok || !Prefs(context).autoStart) return
        Lang.init(context)
        try {
            context.startForegroundService(Intent(context, NasService::class.java))
        } catch (e: Exception) {
            Core.log(L("Không tự khởi động được: $e", "Could not start automatically: $e"))
        }
    }
}
