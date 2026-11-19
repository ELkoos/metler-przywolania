package pl.metler.przywolania

import android.content.Context
import java.util.UUID

class Prefs(private val ctx: Context) {
    private val p = ctx.getSharedPreferences("przywolania", Context.MODE_PRIVATE)

    // Ustawienia domyślne wpisane w APK przy budowie (wariant "bez konfiguracji" do wgrania przez MDM).
    // Ręczna zmiana w ustawieniach ma pierwszeństwo.
    var mode: String
        get() = p.getString("mode", null) ?: autoMode()
        set(v) = p.edit().putString("mode", v).apply()
    var server: String
        get() = p.getString("server", null) ?: BuildConfig.DEFAULT_SERVER
        set(v) = p.edit().putString("server", v).apply()
    var key: String
        get() = p.getString("key", null) ?: BuildConfig.DEFAULT_KEY
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

    /** Bez zapisanego trybu: tylko gdy APK ma wpisany serwer, tryb wg ekranu (tablet = najkrótszy bok ≥ 600 dp). */
    private fun autoMode(): String =
        if (BuildConfig.DEFAULT_SERVER.isEmpty()) ""
        else if (ctx.resources.configuration.smallestScreenWidthDp >= 600) MODE_TABLET else MODE_TERMINAL

    /** Zapisuje ustawienia wpisane w APK, żeby przetrwały aktualizację do APK bez nich. */
    fun seedDefaults() {
        if (BuildConfig.DEFAULT_SERVER.isEmpty() || p.contains("server")) return
        p.edit().putString("server", BuildConfig.DEFAULT_SERVER)
            .putString("key", BuildConfig.DEFAULT_KEY)
            .putString("mode", autoMode())
            .apply()
    }

    // stół tabletu może być pusty: przypisuje go panel serwera
    val configured: Boolean
        get() = (mode == MODE_TABLET || mode == MODE_TERMINAL) && server.isNotBlank() && key.isNotBlank()

    companion object {
        const val MODE_TABLET = "tablet"
        const val MODE_TERMINAL = "terminal"
    }
}
