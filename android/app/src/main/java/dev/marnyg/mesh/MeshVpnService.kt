package dev.marnyg.mesh

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.net.VpnService
import android.util.Log
import mobile.Mobile
import mobile.Tunnel
import org.json.JSONObject

/**
 * Runs nebula on the tun fd this VpnService establishes. Kotlin owns
 * the fd's creation and the route/address plumbing (values parsed from
 * the enrolled config via Mobile.configInfo); Go owns the nebula
 * instance (mobile.Tunnel).
 *
 * Deliberately no DNS server on the tun: the hub's mesh DNS answers
 * only the mesh zone (REFUSED otherwise), and Android sends *all*
 * queries to a VPN-provided resolver — setting it would break every
 * non-mesh lookup on the device. Mesh services are reached by overlay
 * IP (the /hosts list carries name + IP).
 */
class MeshVpnService : VpnService() {
    private var tunnel: Tunnel? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            teardown()
            return START_NOT_STICKY
        }
        if (running) return START_STICKY
        val cfg = Store.config(this)
        if (cfg == null) {
            stopSelf()
            return START_NOT_STICKY
        }

        startForeground(NOTIFICATION_ID, notification())
        try {
            val info = JSONObject(Mobile.configInfo(cfg))
            val ownIP = info.getString("ownIP")
            val prefixLen = info.getInt("prefixLen")
            val pfd = Builder()
                .setSession("talos-mesh")
                .setMtu(info.optInt("mtu", 1300))
                .addAddress(ownIP, prefixLen)
                .addRoute(networkBase(ownIP, prefixLen), prefixLen)
                .establish()
                ?: throw IllegalStateException("VPN consent missing or revoked")
            // detachFd: Go owns the fd from here; nebula closes it on Stop.
            tunnel = Mobile.newTunnel(cfg, pfd.detachFd().toLong(), "")
            running = true
        } catch (e: Exception) {
            Log.e(TAG, "starting tunnel", e)
            teardown()
            return START_NOT_STICKY
        }
        return START_STICKY
    }

    override fun onRevoke() {
        // The user turned on another VPN or pulled consent.
        teardown()
    }

    override fun onDestroy() {
        teardown()
        super.onDestroy()
    }

    private fun teardown() {
        tunnel?.stop()
        tunnel = null
        running = false
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    private fun notification(): Notification {
        val nm = getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(
            NotificationChannel(CHANNEL, getString(R.string.app_name), NotificationManager.IMPORTANCE_LOW)
        )
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE
        )
        return Notification.Builder(this, CHANNEL)
            .setContentTitle(getString(R.string.app_name))
            .setContentText(getString(R.string.status_connected))
            .setSmallIcon(android.R.drawable.ic_lock_lock)
            .setContentIntent(open)
            .setOngoing(true)
            .build()
    }

    /** Network base address for (ip, prefixLen): 10.42.9.9/16 → 10.42.0.0. */
    private fun networkBase(ip: String, prefixLen: Int): String {
        val p = ip.split(".").map { it.toInt() }
        var v = (p[0] shl 24) or (p[1] shl 16) or (p[2] shl 8) or p[3]
        val mask = if (prefixLen == 0) 0 else (-1 shl (32 - prefixLen))
        v = v and mask
        return "${(v ushr 24) and 255}.${(v ushr 16) and 255}.${(v ushr 8) and 255}.${v and 255}"
    }

    companion object {
        private const val TAG = "MeshVpnService"
        private const val CHANNEL = "mesh"
        private const val NOTIFICATION_ID = 1
        const val ACTION_STOP = "dev.marnyg.mesh.STOP"

        /** Read by MainActivity to render the toggle; volatile is enough
         *  for a UI hint (the source of truth is the service itself). */
        @Volatile
        var running = false
            private set
    }
}
