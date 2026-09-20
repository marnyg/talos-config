package dev.marnyg.mesh

import android.content.Context
import java.io.File

/**
 * Where the member lives. The identity-plane state is a directory in
 * app-private storage with the node agent's layout (Go's
 * nodeagent.State: key, kit.json, bundle.json, hub.json, mark) — Go
 * owns every file in it; Kotlin only hands the path over. The key is
 * the one thing that is state (possession is the credential, ADR-0012);
 * the rest is the member's own certs and safe-to-lose caches.
 *
 * SharedPreferences keeps the two user inputs: hub URL and the name
 * the device proposed at enrollment.
 */
object Store {
    private fun prefs(ctx: Context) =
        ctx.getSharedPreferences("mesh", Context.MODE_PRIVATE)

    /** The state dir; created on first use. */
    fun stateDir(ctx: Context): File = File(ctx.filesDir, "member").apply { mkdirs() }

    fun hub(ctx: Context): String =
        prefs(ctx).getString("hub", null) ?: ctx.getString(R.string.default_hub)
    fun setHub(ctx: Context, url: String) =
        prefs(ctx).edit().putString("hub", url).apply()

    fun name(ctx: Context): String? = prefs(ctx).getString("name", null)
    fun setName(ctx: Context, name: String) =
        prefs(ctx).edit().putString("name", name).apply()
}
