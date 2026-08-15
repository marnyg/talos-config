package dev.marnyg.mesh

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.VpnService
import android.util.Log
import java.net.Inet4Address
import mobile.Mobile
import mobile.Tunnel
import org.json.JSONObject

/**
 * Runs nebula on the tun fd this VpnService establishes. Kotlin owns
 * the fd's creation and the route/address plumbing (values parsed from
 * the enrolled config via Mobile.configInfo); Go owns the nebula
 * instance (mobile.Tunnel).
 *
 * DNS: Android sends *all* device queries to a VPN-provided resolver,
 * and the hub answers only the mesh zone — so the resolver we
 * advertise (dnsIP, a magic in-mesh address) is answered by a split
 * shim inside the Go tunnel: mesh-zone names go to the hub through the
 * tunnel, everything else is forwarded on the underlay via sockets
 * protected with VpnService.protect. A sealed hub only costs mesh
 * names, never general DNS. (Task 3b1734db; details in Go dnsshim.go.)
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
            val upstreamDns = underlayDnsServers() // before establish(): "active" must be the underlay
            val pfd = Builder()
                .setSession("talos-mesh")
                .setMtu(info.optInt("mtu", 1300))
                .addAddress(ownIP, prefixLen)
                .addRoute(networkBase(ownIP, prefixLen), prefixLen)
                .addDnsServer(info.getString("dnsIP"))
                .establish()
                ?: throw IllegalStateException("VPN consent missing or revoked")
            // detachFd: Go owns the fd from here; nebula closes it on Stop.
            tunnel = Mobile.newTunnel(cfg, pfd.detachFd().toLong(), "", upstreamDns, protector)
            registerUnderlayCallback()
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
        unregisterUnderlayCallback()
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

    /**
     * The resolvers captured at establish() go stale when the device
     * roams wifi↔cellular; this callback feeds the shim the current
     * underlay's resolvers as links change. The request's default
     * NOT_VPN capability keeps our own tun out of the updates.
     */
    private val underlayCallback = object : ConnectivityManager.NetworkCallback() {
        override fun onLinkPropertiesChanged(network: Network, lp: LinkProperties) {
            val v4 = lp.dnsServers.filterIsInstance<Inet4Address>()
                .mapNotNull { it.hostAddress }
                .joinToString(",")
            if (v4.isNotEmpty()) tunnel?.setUpstreams(v4)
        }
    }
    private var callbackRegistered = false

    private fun registerUnderlayCallback() {
        if (callbackRegistered) return
        getSystemService(ConnectivityManager::class.java).registerNetworkCallback(
            NetworkRequest.Builder()
                .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                .build(),
            underlayCallback
        )
        callbackRegistered = true
    }

    private fun unregisterUnderlayCallback() {
        if (!callbackRegistered) return
        callbackRegistered = false
        try {
            getSystemService(ConnectivityManager::class.java).unregisterNetworkCallback(underlayCallback)
        } catch (_: IllegalArgumentException) {
            // already gone; nothing to release
        }
    }

    /** Marks the shim's underlay DNS sockets as VPN-bypassing. */
    private val protector = object : mobile.SocketProtector {
        override fun protect(fd: Int): Boolean = this@MeshVpnService.protect(fd)
    }

    /**
     * The active network's IPv4 resolvers (comma-separated) for the
     * shim's non-mesh forwards. Must be read before establish(), so
     * "active" is the underlay. Empty is fine — Go falls back to 1.1.1.1.
     */
    private fun underlayDnsServers(): String {
        val cm = getSystemService(ConnectivityManager::class.java)
        val lp = cm.activeNetwork?.let { cm.getLinkProperties(it) } ?: return ""
        return lp.dnsServers.filterIsInstance<Inet4Address>()
            .mapNotNull { it.hostAddress }
            .joinToString(",")
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
