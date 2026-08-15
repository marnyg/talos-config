package dev.marnyg.mesh

import android.content.Context

/**
 * App-private persistence, mirroring nebup's two-file cache (ADR-0012):
 *
 *  - privKey: device-born identity. Survives re-enrollment; the whole
 *    membership credential. Only leaves this store spliced into the
 *    running config.
 *  - config:  disposable hub artifact (CA + cert + spliced key). Any
 *    re-enrollment replaces it; deleting it returns the app to the
 *    enroll screen without changing the device's identity or address.
 *
 * SharedPreferences MODE_PRIVATE is the v1 trust level (same as
 * Mobile Nebula); Android Keystore wrapping is a possible hardening.
 */
object Store {
    private fun prefs(ctx: Context) =
        ctx.getSharedPreferences("mesh", Context.MODE_PRIVATE)

    fun privKey(ctx: Context): String? = prefs(ctx).getString("privKey", null)
    fun setPrivKey(ctx: Context, hex: String) =
        prefs(ctx).edit().putString("privKey", hex).apply()

    fun config(ctx: Context): String? = prefs(ctx).getString("config", null)
    fun setConfig(ctx: Context, yaml: String) =
        prefs(ctx).edit().putString("config", yaml).apply()
    fun clearConfig(ctx: Context) =
        prefs(ctx).edit().remove("config").apply()
}
