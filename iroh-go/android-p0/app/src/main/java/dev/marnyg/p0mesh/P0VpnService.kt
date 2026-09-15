package dev.marnyg.p0mesh

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.net.ConnectivityManager
import android.net.VpnService
import android.util.Log
import java.net.Inet4Address
import p0mobile.P0mobile
import p0mobile.Tunnel

/**
 * P0.2 spike VpnService (docs/mesh-v3-p0.2-android.md step 4). Kotlin owns
 * the tun fd, the routes and the DNS server; Go (p0mobile, gomobile AAR)
 * owns the netstack and the iroh endpoint.
 *
 * Split routing: only the fake range 198.18.0.0/15 enters the tunnel, so
 * everything else on the device — including iroh's own UDP and the
 * underlay DNS forward — never loops back into the tun.
 */
class P0VpnService : VpnService() {
    private var tunnel: Tunnel? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopTunnel(); stopSelf(); return START_NOT_STICKY
        }
        if (tunnel != null) return START_STICKY
        val relay = intent?.getStringExtra(EXTRA_RELAY) ?: return START_NOT_STICKY
        val peer = intent.getStringExtra(EXTRA_PEER) ?: return START_NOT_STICKY
        startForeground(NOTIFICATION_ID, notification())
        try {
            val upstreamDns = underlayDnsServers() // before establish(): "active" must be the underlay
            val pfd = Builder()
                .setSession("p0mesh")
                .setMtu(MTU)
                .addAddress(P0mobile.TunIP, 32)
                .addRoute("198.18.0.0", 15)
                .addDnsServer(P0mobile.ResolverIP)
                .establish() ?: throw IllegalStateException("establish() returned null (VPN permission?)")
            // detachFd: Go owns the fd from here; the netstack reads it until Stop.
            val fd = pfd.detachFd()
            tunnel = P0mobile.start(fd.toLong(), MTU.toLong(), relay, peer, upstreamDns, protector)
            current = this
            Log.i(TAG, "tunnel up id=${tunnel?.id()} upstreamDns=$upstreamDns")
        } catch (e: Exception) {
            Log.e(TAG, "start failed", e)
            lastError = e.toString()
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf()
        }
        return START_STICKY
    }

    override fun onRevoke() { stopTunnel(); stopSelf() }
    override fun onDestroy() { stopTunnel(); super.onDestroy() }

    private fun stopTunnel() {
        tunnel?.let { runCatching { it.stop() }.onFailure { e -> Log.w(TAG, "stop", e) } }
        tunnel = null
        current = null
        stopForeground(STOP_FOREGROUND_REMOVE)
    }

    private val protector = object : p0mobile.SocketProtector {
        override fun protect(fd: Int): Boolean = this@P0VpnService.protect(fd)
    }

    private fun underlayDnsServers(): String {
        val cm = getSystemService(ConnectivityManager::class.java)
        val lp = cm.activeNetwork?.let { cm.getLinkProperties(it) } ?: return ""
        return lp.dnsServers.filterIsInstance<Inet4Address>().joinToString(",") { it.hostAddress + ":53" }
    }

    private fun notification(): Notification {
        val nm = getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(NotificationChannel(CHANNEL, "p0mesh", NotificationManager.IMPORTANCE_LOW))
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE
        )
        return Notification.Builder(this, CHANNEL)
            .setContentTitle("p0mesh tunnel")
            .setContentText("iroh + fake-IP (spike)")
            .setSmallIcon(android.R.drawable.ic_menu_share)
            .setContentIntent(open)
            .setOngoing(true)
            .build()
    }

    companion object {
        const val TAG = "p0mesh"
        const val ACTION_STOP = "dev.marnyg.p0mesh.STOP"
        const val EXTRA_RELAY = "relay"
        const val EXTRA_PEER = "peer"
        const val MTU = 1280
        private const val CHANNEL = "p0mesh"
        private const val NOTIFICATION_ID = 1

        /** The running service, for the activity's stats poll. */
        @Volatile var current: P0VpnService? = null
        @Volatile var lastError: String? = null

        fun statsJson(): String? = current?.tunnel?.statsJSON()
    }
}
