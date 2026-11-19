package pl.metler.przywolania

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.PowerManager
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.json.JSONObject
import java.net.URLEncoder
import java.util.concurrent.TimeUnit

/**
 * Usługa pierwszoplanowa: trzyma stałe połączenie WebSocket z serwerem przywołań,
 * ponawia połączenie, odtwarza alarm przy wezwaniu (tryb Terminal).
 */
class LinkService : Service() {

    private lateinit var prefs: Prefs
    private lateinit var alarm: Alarm
    private val handler = Handler(Looper.getMainLooper())
    private val client = OkHttpClient.Builder()
        .pingInterval(20, TimeUnit.SECONDS)
        .connectTimeout(8, TimeUnit.SECONDS)
        .readTimeout(0, TimeUnit.MILLISECONDS)
        .build()
    private var ws: WebSocket? = null
    private var generation = 0
    private var retryDelay = 1000L
    private var wakeLock: PowerManager.WakeLock? = null
    private var wifiLock: WifiManager.WifiLock? = null
    private var stopping = false
    private var ringingStation: String? = null // stanowisko bieżącego wezwania (wątek główny)

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        instance = this
        prefs = Prefs(this)
        alarm = Alarm(this)
        createChannels()
        val n = statusNotification("Łączenie z serwerem…")
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(NOTIF_STATUS, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(NOTIF_STATUS, n)
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_RECONNECT -> reconnect()
            ACTION_ACK -> ack()
            else -> if (ws == null) connect()
        }
        return START_STICKY
    }

    override fun onDestroy() {
        stopping = true
        generation++
        ws?.close(1000, "stop")
        ws = null
        alarm.stop()
        releaseLocks()
        if (instance === this) instance = null
        AppState.update { connected = false; activeCall = null }
        super.onDestroy()
    }

    // ---------- połączenie ----------

    private fun url(): String {
        var s = prefs.server.trim().removePrefix("http://").removePrefix("ws://").trimEnd('/')
        if (!s.contains(":")) s += ":8080"
        fun e(v: String) = URLEncoder.encode(v, "UTF-8")
        val q = "role=${e(prefs.mode)}&key=${e(prefs.key)}&device=${e(prefs.deviceId)}" +
                if (prefs.mode == Prefs.MODE_TABLET) "&station=${e(prefs.station)}" else ""
        return "ws://$s/ws?$q"
    }

    private fun reconnect() {
        generation++
        ws?.close(1000, "reconnect")
        ws = null
        retryDelay = 1000
        connect()
    }

    private fun connect() {
        if (stopping || !prefs.configured) return
        val gen = ++generation
        val req = try { Request.Builder().url(url()).build() } catch (e: Exception) {
            AppState.update { connected = false; connectError = "Błędny adres serwera" }
            return
        }
        ws = client.newWebSocket(req, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                if (gen != generation) return
                retryDelay = 1000
                AppState.update { connected = true; connectError = "" }
                updateStatusNotification()
                if (prefs.mode == Prefs.MODE_TERMINAL && prefs.workerId.isNotEmpty()) {
                    send(JSONObject().put("t", "login").put("worker", prefs.workerId).put("pin", prefs.workerPin))
                }
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                if (gen != generation) return
                handler.post { onServerMessage(text) }
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                if (gen == generation) lost("Rozłączono")
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                if (gen != generation) return
                val msg = when (response?.code) {
                    401 -> "Zły klucz urządzenia"
                    400 -> "Błędne ustawienia (rola/stanowisko)"
                    else -> "Brak połączenia z serwerem ${prefs.server}"
                }
                lost(msg)
            }
        })
    }

    private fun lost(msg: String) {
        AppState.update { connected = false; connectError = msg }
        updateStatusNotification()
        val gen = generation
        handler.postDelayed({ if (gen == generation && !stopping) connect() }, retryDelay)
        retryDelay = (retryDelay * 2).coerceAtMost(10_000)
    }

    fun send(o: JSONObject): Boolean = ws?.send(o.toString()) ?: false

    // ---------- wiadomości z serwera ----------

    private fun onServerMessage(text: String) {
        val m = try { JSONObject(text) } catch (_: Exception) { return }
        when (m.optString("t")) {
            "workers" -> {
                val arr = m.optJSONArray("list")
                val list = mutableListOf<PublicWorker>()
                if (arr != null) for (i in 0 until arr.length()) {
                    val w = arr.getJSONObject(i)
                    list.add(PublicWorker(w.getString("id"), w.getString("name"), w.optBoolean("hasPin")))
                }
                AppState.update { workers = list }
            }
            "login_ok" -> {
                val name = m.optJSONObject("worker")?.optString("name") ?: ""
                AppState.update { loggedInName = name; loginError = "" }
                acquireLocks()
                updateStatusNotification()
            }
            "login_err" -> {
                prefs.workerId = ""; prefs.workerPin = ""
                AppState.update { loggedInName = ""; loginError = m.optString("reason") }
                releaseLocks()
            }
            "logged_out" -> {
                prefs.workerId = ""; prefs.workerPin = ""
                stopCall()
                val r = m.optString("reason")
                AppState.update { loggedInName = ""; if (r.isNotEmpty()) toast = r }
                releaseLocks()
                updateStatusNotification()
            }
            "call" -> {
                val c = CallInfo(m.getString("id"), m.optString("station"), m.optLong("at"))
                val prevStation = ringingStation
                ringingStation = c.station
                AppState.update { activeCall = c }
                // to samo wezwanie (np. po ponownym połączeniu) albo kolejne ze stanowiska, gdy ekran już dzwoni:
                // tylko aktualizacja, bez ponownego otwierania ekranu
                if (prevStation != c.station) startCall(c)
            }
            "call_cancel" -> {
                if (AppState.activeCall?.id == m.optString("id")) stopCall()
            }
            "state" -> {
                val arr = m.optJSONArray("workers")
                val list = mutableListOf<WorkerState>()
                if (arr != null) for (i in 0 until arr.length()) {
                    val w = arr.getJSONObject(i)
                    list.add(WorkerState(w.getString("id"), w.getString("name"), w.getString("status"),
                        w.optString("station"), w.optLong("since"), w.optBoolean("online")))
                }
                val offset = m.optLong("now", System.currentTimeMillis()) - System.currentTimeMillis()
                AppState.update { tabletWorkers = list; serverTimeOffset = offset }
            }
            "call_result" -> {
                val msg = if (!m.optBoolean("ok")) m.optString("reason") else m.optString("warning")
                if (msg.isNotEmpty()) AppState.update { toast = msg }
            }
        }
    }

    // ---------- wezwanie (terminal) ----------

    private fun startCall(c: CallInfo) {
        alarm.start()
        val open = Intent(this, CallActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        val pi = PendingIntent.getActivity(this, 1, open, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        val ackPi = PendingIntent.getBroadcast(this, 2, Intent(this, AckReceiver::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        val n = Notification.Builder(this, CH_CALL)
            .setSmallIcon(R.drawable.ic_notif)
            .setContentTitle("PODEJDŹ DO STOLIKA")
            .setContentText(c.station)
            .setCategory(Notification.CATEGORY_ALARM)
            .setOngoing(true)
            .setFullScreenIntent(pi, true)
            .setContentIntent(pi)
            .addAction(Notification.Action.Builder(null, "IDĘ", ackPi).build())
            .build()
        nm().notify(NOTIF_CALL, n)
        // Pokaż ekran wezwania od razu (działa, gdy aplikacja ma zgodę "wyświetlanie nad innymi aplikacjami"
        // albo jest na pierwszym planie; w innym wypadku zadziała powiadomienie pełnoekranowe).
        try { startActivity(open) } catch (_: Exception) {}
    }

    private fun stopCall() {
        ringingStation = null
        alarm.stop()
        nm().cancel(NOTIF_CALL)
        AppState.update { activeCall = null }
    }

    fun ack() {
        val c = AppState.activeCall ?: run { alarm.stop(); nm().cancel(NOTIF_CALL); return }
        send(JSONObject().put("t", "ack").put("id", c.id))
        stopCall()
    }

    // ---------- blokady (terminal zalogowany) ----------

    private fun acquireLocks() {
        if (prefs.mode != Prefs.MODE_TERMINAL) return
        if (wakeLock == null) {
            wakeLock = (getSystemService(Context.POWER_SERVICE) as PowerManager)
                .newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "przywolania:polaczenie").apply {
                    setReferenceCounted(false); acquire()
                }
        }
        if (wifiLock == null) {
            @Suppress("DEPRECATION")
            wifiLock = (applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager)
                .createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "przywolania:wifi").apply {
                    setReferenceCounted(false); acquire()
                }
        }
    }

    private fun releaseLocks() {
        try { wakeLock?.release() } catch (_: Exception) {}
        try { wifiLock?.release() } catch (_: Exception) {}
        wakeLock = null; wifiLock = null
    }

    // ---------- powiadomienia ----------

    private fun nm() = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager

    private fun createChannels() {
        val status = NotificationChannel(CH_STATUS, "Połączenie z serwerem", NotificationManager.IMPORTANCE_LOW)
        val call = NotificationChannel(CH_CALL, "Wezwania", NotificationManager.IMPORTANCE_HIGH).apply {
            setSound(null, null) // dźwięk odtwarza aplikacja (pętla do potwierdzenia)
            enableVibration(false)
            lockscreenVisibility = Notification.VISIBILITY_PUBLIC
            setBypassDnd(true)
        }
        nm().createNotificationChannel(status)
        nm().createNotificationChannel(call)
    }

    private fun statusNotification(text: String): Notification {
        val pi = PendingIntent.getActivity(this, 0, Intent(this, StartActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        return Notification.Builder(this, CH_STATUS)
            .setSmallIcon(R.drawable.ic_notif)
            .setContentTitle(if (prefs.mode == Prefs.MODE_TABLET) "Przywołania – ${prefs.station}" else "Przywołania")
            .setContentText(text)
            .setOngoing(true)
            .setContentIntent(pi)
            .build()
    }

    private fun updateStatusNotification() {
        handler.post {
            val text = when {
                !AppState.connected -> AppState.connectError.ifEmpty { "Łączenie z serwerem…" }
                prefs.mode == Prefs.MODE_TERMINAL && AppState.loggedInName.isNotEmpty() -> "Zalogowany: ${AppState.loggedInName}"
                prefs.mode == Prefs.MODE_TERMINAL -> "Połączono – nikt nie jest zalogowany"
                else -> "Połączono"
            }
            nm().notify(NOTIF_STATUS, statusNotification(text))
        }
    }

    companion object {
        const val CH_STATUS = "status"
        const val CH_CALL = "wezwania"
        const val NOTIF_STATUS = 1
        const val NOTIF_CALL = 2
        const val ACTION_RECONNECT = "reconnect"
        const val ACTION_ACK = "ack"

        @Volatile var instance: LinkService? = null

        fun start(ctx: Context, action: String? = null) {
            val i = Intent(ctx, LinkService::class.java)
            if (action != null) i.action = action
            ctx.startForegroundService(i)
        }

        fun stop(ctx: Context) { ctx.stopService(Intent(ctx, LinkService::class.java)) }

        fun send(o: JSONObject): Boolean = instance?.send(o) ?: false
    }
}
