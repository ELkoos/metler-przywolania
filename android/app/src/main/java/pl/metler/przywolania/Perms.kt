package pl.metler.przywolania

import android.app.Activity
import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.PowerManager
import android.provider.Settings

/** Uprawnienia potrzebne do pewnego działania; lista brakujących z akcją włączenia. */
data class PermIssue(val label: String, val fix: (Activity) -> Unit)

fun Context.permIssues(terminal: Boolean): List<PermIssue> {
    val out = mutableListOf<PermIssue>()
    val nm = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
    if (Build.VERSION.SDK_INT >= 33 && !nm.areNotificationsEnabled()) {
        out.add(PermIssue("Powiadomienia") { it.requestPermissions(arrayOf(android.Manifest.permission.POST_NOTIFICATIONS), 1) })
    }
    val pm = getSystemService(Context.POWER_SERVICE) as PowerManager
    if (!pm.isIgnoringBatteryOptimizations(packageName)) {
        out.add(PermIssue("Praca w tle bez ograniczeń baterii") {
            it.startActivity(Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:$packageName")))
        })
    }
    if (terminal) {
        if (!Settings.canDrawOverlays(this)) {
            out.add(PermIssue("Wyświetlanie nad innymi aplikacjami") {
                it.startActivity(Intent(Settings.ACTION_MANAGE_OVERLAY_PERMISSION, Uri.parse("package:$packageName")))
            })
        }
        if (Build.VERSION.SDK_INT >= 34 && !nm.canUseFullScreenIntent()) {
            out.add(PermIssue("Powiadomienia pełnoekranowe") {
                it.startActivity(Intent(Settings.ACTION_MANAGE_APP_USE_FULL_SCREEN_INTENT, Uri.parse("package:$packageName")))
            })
        }
    }
    return out
}
