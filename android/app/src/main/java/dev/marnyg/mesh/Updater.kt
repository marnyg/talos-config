package dev.marnyg.mesh

import android.app.Activity
import android.app.AlertDialog
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

/**
 * The update check. The app is sideloaded from the rolling
 * `android-latest` release (android/publish.sh), so nothing tells a
 * device a newer build exists; a stale client then fails in ways that
 * look like the mesh's fault (a facet it cannot read, an enrollment
 * message the hub no longer takes).
 *
 * publish.sh uploads a small manifest beside the APK — `{versionCode,
 * versionName, sha}` — and this compares its versionCode (the commit
 * count the APK was built at, app/build.gradle.kts) with the installed
 * one. Newer ⇒ one dialog; "Download" hands the APK's URL to the
 * browser, and Android's own installer takes it from there. The app
 * installs nothing itself: no REQUEST_INSTALL_PACKAGES, and the gate
 * on what may replace it stays Android's signature check — the
 * manifest is a hint, never authority.
 *
 * Quiet by default: at most one successful check per CHECK_EVERY_MS,
 * failures (offline, GitHub down) are silent and retried later, and
 * nothing here may throw into the activity.
 */
object Updater {
    private const val CHECK_EVERY_MS = 12 * 60 * 60 * 1000L
    private const val RETRY_AFTER_MS = 5 * 60 * 1000L
    private const val TIMEOUT_MS = 10_000
    private const val MAX_MANIFEST_BYTES = 16 * 1024
    private const val PREF_CHECKED_AT = "update_checked_at"

    data class Release(val versionCode: Long, val versionName: String)

    // Per process: the last attempt (throttles retries after a failure)
    // and whether a dialog is up (onResume fires again behind it).
    @Volatile private var lastAttempt = 0L
    @Volatile private var showing = false

    private fun prefs(ctx: Context) =
        ctx.getSharedPreferences("mesh", Context.MODE_PRIVATE)

    /** What is installed, from the package manager (no BuildConfig). */
    fun installed(ctx: Context): Release {
        val info = ctx.packageManager.getPackageInfo(ctx.packageName, 0)
        @Suppress("DEPRECATION")
        val code = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) info.longVersionCode
        else info.versionCode.toLong()
        return Release(code, info.versionName ?: "?")
    }

    /** Fetches and parses the manifest. Blocking: call off the main thread. */
    private fun published(url: String): Release {
        val conn = URL(url).openConnection() as HttpURLConnection
        try {
            conn.connectTimeout = TIMEOUT_MS
            conn.readTimeout = TIMEOUT_MS
            conn.instanceFollowRedirects = true // the release asset URL redirects to the CDN
            conn.setRequestProperty("Accept", "application/json")
            if (conn.responseCode != HttpURLConnection.HTTP_OK) {
                throw IllegalStateException("HTTP ${conn.responseCode}")
            }
            val body = conn.inputStream.use { input ->
                val buf = ByteArray(MAX_MANIFEST_BYTES + 1)
                var n = 0
                while (n < buf.size) {
                    val r = input.read(buf, n, buf.size - n)
                    if (r < 0) break
                    n += r
                }
                if (n > MAX_MANIFEST_BYTES) throw IllegalStateException("manifest too large")
                String(buf, 0, n, Charsets.UTF_8)
            }
            val j = JSONObject(body)
            val code = j.getLong("versionCode")
            if (code <= 0) throw IllegalStateException("versionCode $code")
            return Release(code, j.optString("versionName", code.toString()))
        } finally {
            conn.disconnect()
        }
    }

    /**
     * Checks and, if the published build is newer, prompts. Without
     * [force] it is throttled and silent unless there is an update;
     * with it (the Debug screen's button) it always asks the network
     * and reports the outcome through [report].
     */
    fun check(
        activity: Activity,
        scope: CoroutineScope,
        force: Boolean = false,
        report: ((String) -> Unit)? = null,
    ) {
        val now = System.currentTimeMillis()
        if (!force) {
            val checkedAt = prefs(activity).getLong(PREF_CHECKED_AT, 0L)
            // A clock set backwards must not silence the check forever.
            if (checkedAt in (now - CHECK_EVERY_MS + 1)..now) return
            if (now - lastAttempt in 0 until RETRY_AFTER_MS) return
        }
        if (showing) return
        lastAttempt = now
        val manifestUrl = activity.getString(R.string.update_manifest_url)
        scope.launch {
            try {
                val have = installed(activity)
                val latest = withContext(Dispatchers.IO) { published(manifestUrl) }
                prefs(activity).edit().putLong(PREF_CHECKED_AT, System.currentTimeMillis()).apply()
                if (latest.versionCode > have.versionCode) {
                    report?.invoke(activity.getString(R.string.update_available_fmt, have.versionName, latest.versionName))
                    prompt(activity, have, latest)
                } else {
                    report?.invoke(activity.getString(R.string.update_none_fmt, have.versionName, have.versionCode))
                }
            } catch (e: CancellationException) {
                throw e // the activity went away
            } catch (e: Exception) {
                // Offline, GitHub unreachable, a malformed manifest: not
                // the user's problem unless they asked.
                report?.invoke(activity.getString(R.string.update_check_failed, e.message ?: e.toString()))
            }
        }
    }

    private fun prompt(activity: Activity, have: Release, latest: Release) {
        if (showing || activity.isFinishing || activity.isDestroyed) return
        showing = true
        AlertDialog.Builder(activity)
            .setTitle(R.string.update_title)
            .setMessage(activity.getString(R.string.update_message_fmt, have.versionName, latest.versionName))
            .setPositiveButton(R.string.update_download) { _, _ -> download(activity) }
            .setNegativeButton(R.string.update_later, null)
            .setOnDismissListener { showing = false }
            .show()
    }

    /**
     * Hands the APK's URL to whatever opens https links. A TV usually
     * has no browser: say where the file is instead (the Downloader
     * app's address, android/README.md).
     */
    private fun download(activity: Activity) {
        val url = activity.getString(R.string.update_apk_url)
        try {
            activity.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)))
        } catch (e: ActivityNotFoundException) {
            if (activity.isFinishing || activity.isDestroyed) return
            AlertDialog.Builder(activity)
                .setMessage(activity.getString(R.string.update_no_browser_fmt, url))
                .setPositiveButton(android.R.string.ok, null)
                .show()
        }
    }
}
