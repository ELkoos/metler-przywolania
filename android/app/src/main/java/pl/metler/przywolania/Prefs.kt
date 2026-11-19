package pl.metler.przywolania

import android.content.Context
import java.util.UUID

class Prefs(ctx: Context) {
    private val p = ctx.getSharedPreferences("przywolania", Context.MODE_PRIVATE)

    var mode: String
        get() = p.getString("mode", "") ?: ""
        set(v) = p.edit().putString("mode", v).apply()
    var server: String
        get() = p.getString("server", "") ?: ""
        set(v) = p.edit().putString("server", v).apply()
    var key: String
        get() = p.getString("key", "") ?: ""
        set(v) = p.edit().putString("key", v).apply()
    var station: String
        get() = p.getString("station", "") ?: ""
        set(v) = p.edit().putString("station", v).apply()
    var workerId: String
        get() = p.getString("workerId", "") ?: ""
        set(v) = p.edit().putString("workerId", v).apply()
    var workerPin: String
        get() = p.getString("workerPin", "") ?: ""
        set(v) = p.edit().putString("workerPin", v).apply()

    val deviceId: String
        get() {
            var id = p.getString("deviceId", null)
            if (id == null) {
                id = UUID.randomUUID().toString().substring(0, 8)
                p.edit().putString("deviceId", id).apply()
            }
            return id
        }

    val configured: Boolean
        get() = (mode == MODE_TABLET && server.isNotBlank() && key.isNotBlank() && station.isNotBlank()) ||
                (mode == MODE_TERMINAL && server.isNotBlank() && key.isNotBlank())

    companion object {
        const val MODE_TABLET = "tablet"
        const val MODE_TERMINAL = "terminal"
    }
}
