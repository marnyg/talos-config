package dev.marnyg.mesh

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.net.VpnService

/**
 * TV autostart: reconnect the mesh on boot so the Jellyfin app works
 * without anyone finding this app first. Only fires when the device is
 * enrolled AND the VPN consent is still on file — otherwise starting
 * the service would just die on establish(), so we stay quiet and let
 * the user reconnect from the UI.
 */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(ctx: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_BOOT_COMPLETED) return
        if (Store.config(ctx) == null) return
        if (VpnService.prepare(ctx) != null) return
        ctx.startForegroundService(Intent(ctx, MeshVpnService::class.java))
    }
}
