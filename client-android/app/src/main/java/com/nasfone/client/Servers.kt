package com.nasfone.client

import android.content.Context
import android.os.Build
import com.nasfone.core.mobileclient.Mobileclient
import com.nasfone.core.mobileclient.Session
import org.json.JSONArray
import org.json.JSONObject
import java.util.concurrent.ConcurrentHashMap

/** One paired server as the app remembers it. Nothing here is secret. */
data class Server(
    val id: String,            // the device ID the server gave this phone
    val host: String,          // shown name, e.g. nasfone.example.ts.net
    val config: String,        // core config JSON (server key, URL, role…)
    val keyAlias: String,      // Keystore alias of this phone's key for that server
    var addrs: String = "[]",  // addresses learned from the server, fastest first
    var role: String = "user",
    var revoked: Boolean = false,
    var backup: Boolean = false,       // automatic photo/video backup
    var backupWifiOnly: Boolean = true,
    var backupSince: Long = 0,         // only media added after this (unix seconds)
) {
    fun toJson(): JSONObject = JSONObject()
        .put("id", id).put("host", host).put("config", config).put("keyAlias", keyAlias)
        .put("addrs", addrs).put("role", role).put("revoked", revoked)
        .put("backup", backup).put("backupWifiOnly", backupWifiOnly).put("backupSince", backupSince)

    val canWrite get() = role == "admin"

    companion object {
        fun fromJson(o: JSONObject) = Server(
            id = o.getString("id"), host = o.optString("host"), config = o.getString("config"),
            keyAlias = o.getString("keyAlias"), addrs = o.optString("addrs", "[]"), role = o.optString("role", "user"),
            revoked = o.optBoolean("revoked"), backup = o.optBoolean("backup"),
            backupWifiOnly = o.optBoolean("backupWifiOnly", true), backupSince = o.optLong("backupSince"),
        )
    }
}

/** The paired servers and their live sessions. */
object Servers {
    private val sessions = ConcurrentHashMap<String, Session>()
    private val lock = Any()

    private fun prefs(ctx: Context) = ctx.applicationContext.getSharedPreferences("nasfone", Context.MODE_PRIVATE)

    fun list(ctx: Context): List<Server> = synchronized(lock) {
        val arr = try { JSONArray(prefs(ctx).getString("servers", "[]")) } catch (_: Exception) { JSONArray() }
        (0 until arr.length()).map { Server.fromJson(arr.getJSONObject(it)) }
    }

    fun get(ctx: Context, id: String): Server? = list(ctx).firstOrNull { it.id == id }

    private fun saveAll(ctx: Context, list: List<Server>) {
        val arr = JSONArray()
        list.forEach { arr.put(it.toJson()) }
        prefs(ctx).edit().putString("servers", arr.toString()).apply()
    }

    fun put(ctx: Context, s: Server) = synchronized(lock) {
        val all = list(ctx).filter { it.id != s.id } + s
        saveAll(ctx, all)
    }

    fun update(ctx: Context, id: String, f: (Server) -> Unit) = synchronized(lock) {
        val all = list(ctx)
        all.firstOrNull { it.id == id }?.let(f)
        saveAll(ctx, all)
    }

    fun remove(ctx: Context, id: String) = synchronized(lock) {
        val s = get(ctx, id) ?: return@synchronized
        saveAll(ctx, list(ctx).filter { it.id != id })
        sessions.remove(id)
        KeystoreKey.delete(s.keyAlias)
    }

    /** The session for a server, created on first use (does not connect yet). */
    fun session(ctx: Context, id: String): Session {
        sessions[id]?.let { return it }
        val s = get(ctx, id) ?: throw IllegalStateException("unknown server")
        val sess = Mobileclient.newSession(s.config, s.addrs, KeystoreKey(s.keyAlias))
        sessions[id] = sess
        return sess
    }

    /**
     * Signs in if needed and remembers what the server said (role, addresses).
     * Returns {"base","via","role"}. Throws with a message starting "revoked:"
     * when the server no longer knows this phone.
     */
    fun connect(ctx: Context, id: String, fresh: Boolean = false): JSONObject {
        val sess = session(ctx, id)
        try {
            val info = JSONObject(if (fresh) sess.reconnect() else sess.connect())
            update(ctx, id) {
                it.addrs = sess.addrs()
                it.role = info.optString("role", it.role)
                it.revoked = false
            }
            return info
        } catch (e: Exception) {
            if (isRevoked(e)) update(ctx, id) { it.revoked = true; it.backup = false }
            throw e
        }
    }

    /** After a network change: pick the best path again on the next request. */
    fun networkChanged() {
        for (s in sessions.values) Thread { try { s.reconnect() } catch (_: Exception) {} }.start()
    }

    fun isRevoked(e: Throwable) = e.message?.startsWith("revoked:") == true

    fun deviceName(): String {
        val m = Build.MODEL ?: "Android"
        val brand = Build.MANUFACTURER?.replaceFirstChar { it.uppercase() } ?: ""
        return "Android – " + if (m.startsWith(brand, ignoreCase = true)) m else "$brand $m".trim()
    }
}
