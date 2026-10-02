package com.nasfone.client

import android.Manifest
import android.app.job.JobInfo
import android.app.job.JobParameters
import android.app.job.JobScheduler
import android.app.job.JobService
import android.content.ComponentName
import android.content.ContentUris
import android.content.Context
import android.content.pm.PackageManager
import android.net.ConnectivityManager
import android.net.Uri
import android.os.Build
import android.os.ParcelFileDescriptor
import android.provider.MediaStore
import com.nasfone.core.mobileclient.Mobileclient
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlin.concurrent.thread

/**
 * Automatic photo & video backup to every server that has it switched on.
 * New media go to "/Camera Backup/<phone>/<yyyy-MM>/<name>"; a file with the
 * same name already there is skipped, so re-runs never duplicate.
 *
 * Runs as a JobService: when new media appear (content trigger), every ~15
 * minutes as a fallback, and on demand. Each run stops after ~8 minutes and
 * picks up where it left off next time.
 */
object Backup {
    private const val JOB_PERIODIC = 1
    private const val JOB_TRIGGER = 2
    private const val JOB_NOW = 3
    private const val MAX_RUN_MS = 8 * 60 * 1000L

    fun mediaPermissions(): Array<String> = if (Build.VERSION.SDK_INT >= 33)
        arrayOf(Manifest.permission.READ_MEDIA_IMAGES, Manifest.permission.READ_MEDIA_VIDEO, Manifest.permission.POST_NOTIFICATIONS)
    else arrayOf(Manifest.permission.READ_EXTERNAL_STORAGE)

    fun hasMediaPermission(ctx: Context): Boolean {
        val needed = if (Build.VERSION.SDK_INT >= 33)
            listOf(Manifest.permission.READ_MEDIA_IMAGES, Manifest.permission.READ_MEDIA_VIDEO)
        else listOf(Manifest.permission.READ_EXTERNAL_STORAGE)
        return needed.all { ctx.checkSelfPermission(it) == PackageManager.PERMISSION_GRANTED }
    }

    private fun scheduler(ctx: Context) = ctx.getSystemService(JobScheduler::class.java)
    private fun component(ctx: Context) = ComponentName(ctx, BackupJob::class.java)

    private fun enabled(ctx: Context) =
        if (com.nasfone.server.Role.get(ctx) != com.nasfone.server.Role.CLIENT) emptyList()
        else Servers.list(ctx).filter { it.backup && it.canWrite && !it.revoked }

    /** (Re)schedules the jobs to match the servers' settings. */
    fun schedule(ctx: Context) {
        val js = scheduler(ctx)
        val servers = enabled(ctx)
        if (servers.isEmpty()) {
            js.cancel(JOB_PERIODIC)
            js.cancel(JOB_TRIGGER)
            return
        }
        // Wi-Fi only if every server asks for it; per-server checks happen in the run.
        val net = if (servers.all { it.backupWifiOnly }) JobInfo.NETWORK_TYPE_UNMETERED else JobInfo.NETWORK_TYPE_ANY
        js.schedule(JobInfo.Builder(JOB_PERIODIC, component(ctx))
            .setRequiredNetworkType(net)
            .setPeriodic(15 * 60 * 1000L)
            .setPersisted(true)
            .build())
        scheduleTrigger(ctx, net)
    }

    /** A one-shot job that fires when new photos or videos are added. */
    private fun scheduleTrigger(ctx: Context, net: Int) {
        val b = JobInfo.Builder(JOB_TRIGGER, component(ctx))
            .setRequiredNetworkType(net)
            .addTriggerContentUri(JobInfo.TriggerContentUri(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, JobInfo.TriggerContentUri.FLAG_NOTIFY_FOR_DESCENDANTS))
            .addTriggerContentUri(JobInfo.TriggerContentUri(MediaStore.Video.Media.EXTERNAL_CONTENT_URI, JobInfo.TriggerContentUri.FLAG_NOTIFY_FOR_DESCENDANTS))
            .setTriggerContentUpdateDelay(10_000)
            .setTriggerContentMaxDelay(60_000)
        scheduler(ctx).schedule(b.build())
    }

    fun runNow(ctx: Context) {
        scheduler(ctx).schedule(JobInfo.Builder(JOB_NOW, component(ctx))
            .setRequiredNetworkType(JobInfo.NETWORK_TYPE_ANY)
            .build())
    }

    private fun prefs(ctx: Context) = ctx.getSharedPreferences("nasfone", Context.MODE_PRIVATE)

    fun statusText(ctx: Context, id: String): String =
        prefs(ctx).getString("backupStatus_$id", null) ?: L("Chưa chạy lần nào.", "Not run yet.")

    private fun setStatus(ctx: Context, id: String, s: String) = prefs(ctx).edit().putString("backupStatus_$id", s).apply()

    private class Media(val uri: Uri, val name: String, val size: Long, val added: Long, val taken: Long)

    private fun query(ctx: Context, collection: Uri, since: Long): List<Media> {
        val out = mutableListOf<Media>()
        val cols = arrayOf(MediaStore.MediaColumns._ID, MediaStore.MediaColumns.DISPLAY_NAME, MediaStore.MediaColumns.SIZE,
            MediaStore.MediaColumns.DATE_ADDED, MediaStore.MediaColumns.DATE_TAKEN)
        ctx.contentResolver.query(collection, cols, "${MediaStore.MediaColumns.DATE_ADDED} >= ?", arrayOf(since.toString()),
            "${MediaStore.MediaColumns.DATE_ADDED} ASC")?.use { c ->
            while (c.moveToNext()) {
                val name = c.getString(1) ?: continue
                out += Media(ContentUris.withAppendedId(collection, c.getLong(0)), name, c.getLong(2), c.getLong(3),
                    if (c.isNull(4)) 0 else c.getLong(4))
            }
        }
        return out
    }

    /** One backup pass over all servers. Returns true when it stopped early (more to do). */
    fun run(ctx: Context, shouldStop: () -> Boolean): Boolean {
        if (!hasMediaPermission(ctx)) return false
        val started = System.currentTimeMillis()
        val metered = ctx.getSystemService(ConnectivityManager::class.java).isActiveNetworkMetered
        val folder = "/Camera Backup/" + Servers.deviceName().removePrefix("Android – ").replace('/', '_')
        val month = SimpleDateFormat("yyyy-MM", Locale.US)
        val stamp = SimpleDateFormat("HH:mm dd/MM", Locale.US)
        for (s in enabled(ctx)) {
            if (s.backupWifiOnly && metered) {
                setStatus(ctx, s.id, L("Đang chờ Wi-Fi…", "Waiting for Wi-Fi…"))
                continue
            }
            val sess = try { Servers.connect(ctx, s.id); Servers.session(ctx, s.id) } catch (e: Exception) {
                setStatus(ctx, s.id, "⚠ " + friendly(e))
                continue
            }
            val media = (query(ctx, MediaStore.Images.Media.EXTERNAL_CONTENT_URI, s.backupSince) +
                query(ctx, MediaStore.Video.Media.EXTERNAL_CONTENT_URI, s.backupSince)).sortedBy { it.added }
            val made = HashSet<String>()
            var done = 0
            var skipped = 0
            for ((i, m) in media.withIndex()) {
                if (shouldStop() || System.currentTimeMillis() - started > MAX_RUN_MS) {
                    setStatus(ctx, s.id, L("Đang sao lưu… ${media.size - i} file còn lại", "Backing up… ${media.size - i} files left"))
                    return true
                }
                val dir = "$folder/" + month.format(Date(if (m.taken > 0) m.taken else m.added * 1000))
                try {
                    if (made.add(dir)) {
                        // Create the folders on the way; "already exists" is fine.
                        var p = ""
                        for (part in dir.trim('/').split('/')) {
                            p += "/$part"
                            try { sess.mkdir(p) } catch (_: Exception) {}
                        }
                    }
                    Notifier.progress(ctx, i + 1, media.size, s.host)
                    val pfd = ctx.contentResolver.openFileDescriptor(m.uri, "r") ?: continue
                    val written = sess.upload("$dir/${m.name}", pfd.detachFd().toLong(), m.size, Mobileclient.ModeSkip, null)
                    if (written.isEmpty()) skipped++ else done++
                    Servers.update(ctx, s.id) { it.backupSince = m.added } // resume point
                } catch (e: Exception) {
                    setStatus(ctx, s.id, "⚠ ${m.name}: " + friendly(e))
                    if (Servers.isRevoked(e)) break
                    Notifier.done(ctx)
                    return true // network trouble: retry later from this file
                }
            }
            Notifier.done(ctx)
            if (media.isNotEmpty() || prefs(ctx).getString("backupStatus_${s.id}", null) == null) {
                setStatus(ctx, s.id, L("Lần cuối: ${stamp.format(Date())} • tải lên $done, đã có $skipped",
                    "Last run: ${stamp.format(Date())} • uploaded $done, already there $skipped"))
            }
        }
        return false
    }
}

/** The JobService behind Backup. */
class BackupJob : JobService() {
    @Volatile
    private var stopped = false

    override fun onStartJob(params: JobParameters): Boolean {
        stopped = false
        Lang.init(this)
        thread(name = "nasfone-backup") {
            val more = try { Backup.run(this) { stopped } } catch (_: Exception) { true }
            if (params.jobId == 2) Backup.schedule(this) // content triggers are one-shot
            jobFinished(params, more)
        }
        return true
    }

    override fun onStopJob(params: JobParameters): Boolean {
        stopped = true
        return true // retry later
    }
}
