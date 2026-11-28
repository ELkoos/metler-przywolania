package pl.metler.przywolania

import android.os.Handler
import android.os.Looper

data class PublicWorker(val id: String, val name: String, val hasPin: Boolean)
data class WorkerState(
    val id: String, val name: String, val status: String,
    val station: String, val since: Long, val online: Boolean,
    val escalated: Boolean = false
)
data class CallInfo(val id: String, val station: String, val at: Long)
/** Alert dla brygadzisty: wezwany pracownik nie odpowiada. */
data class AlertInfo(val id: String, val station: String, val worker: String, val at: Long)

/** Wspólny stan aplikacji, obserwowany przez ekrany. Zmiany tylko w wątku głównym. */
object AppState {
    var connected = false
    var connectError: String = ""
    var workers: List<PublicWorker> = emptyList()       // terminal: lista do logowania
    var loggedInName: String = ""                        // terminal: zalogowany pracownik
    var loginError: String = ""
    var tabletWorkers: List<WorkerState> = emptyList()   // tablet: zalogowani pracownicy
    var serverTimeOffset: Long = 0                       // serwer - lokalnie, ms
    var activeCall: CallInfo? = null                     // terminal: bieżące wezwanie
    var alerts: List<AlertInfo> = emptyList()            // terminal brygadzisty: wezwania bez odpowiedzi
    var goingTo: String = ""                             // terminal: stół, do którego pracownik idzie (po "IDĘ")
    var goingUntil: Long = 0                             // do kiedy pokazywać "Idziesz do" (czas lokalny, ms)
    var toast: String = ""

    private val main = Handler(Looper.getMainLooper())
    private val listeners = mutableSetOf<() -> Unit>()

    fun listen(l: () -> Unit) { listeners.add(l) }
    fun unlisten(l: () -> Unit) { listeners.remove(l) }

    fun update(block: AppState.() -> Unit) {
        main.post {
            block()
            listeners.toList().forEach { it() }
        }
    }

    fun takeToast(): String { val t = toast; toast = ""; return t }
}
