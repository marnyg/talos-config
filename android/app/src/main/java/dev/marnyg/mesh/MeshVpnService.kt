package dev.marnyg.mesh

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.VpnService
import android.os.ParcelFileDescriptor
import android.util.Log
import java.io.File
import java.net.Inet4Address
import mobile.Mobile
import mobile.Tunnel

/**
 * Runs the member on the tun fd this VpnService establishes (Mesh v3
 * P2.4). Kotlin owns the fd's creation and the route/address/DNS
 * plumbing; Go (mobile.Tunnel: nodeagent + meshtun) owns everything
 * on it.
 *
 * Split routing: only the fake range 198.18.0.0/15 enters the tunnel,
 * so everything else on the device — iroh's own UDP, the underlay DNS
 * forward — never loops back into the tun. Android still sends *all*
 * DNS to the VPN's resolver, so the fake resolver at 198.18.0.2
 * answers *.mesh.internal from the plane's name map and forwards
 * everything else to the underlay's resolvers through protect()ed
 * sockets.
 *
 * One VpnService per device (P2.4 finding .1): starting this evicts
 * any other VPN; MainActivity says so before asking for consent.
 */
class MeshVpnService : VpnService() {
    private var tunnel: Tunnel? = null
    private var pfd: ParcelFileDescriptor? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            teardown()
            return START_NOT_STICKY
        }
        if (running) return START_STICKY
        val stateDir = Store.stateDir(this).absolutePath
        if (!Mobile.enrolled(stateDir)) {
            stopSelf()
            return START_NOT_STICKY
        }

        startForeground(NOTIFICATION_ID, notification())
        try {
            // One session per log file: Go appends from here (debug
            // screen shows this session, or the last one after a stop).
            val log = logFile(this).apply { delete() }
            val upstreamDns = underlayDnsServers() // before establish(): "active" must be the underlay
            val localAddrs = underlayAddresses()   // idem
            val fd = Builder()
                .setSession("talos-mesh")
                .setMtu(MTU)
                .addAddress(Mobile.TunIP, 32)
                .addRoute(Mobile.FakeRange, Mobile.FakePrefixLen.toInt())
                .addDnsServer(Mobile.ResolverIP)
                .establish()
                ?: throw IllegalStateException("VPN consent missing or revoked")
            pfd = fd
            // detachFd: Go reads the fd until Stop; we close it after.
            tunnel = Mobile.start(
                stateDir, Store.hub(this), "",
                fd.detachFd().toLong(), MTU.toLong(),
                upstreamDns, localAddrs, log.absolutePath, protector
            )
            registerUnderlayCallback()
            instance = this
            lastError = null
            running = true
            Log.i(TAG, "tunnel up node=${tunnel?.nodeID()} upstreamDns=$upstreamDns localAddrs=$localAddrs")
        } catch (e: Exception) {
            Log.e(TAG, "starting tunnel", e)
            // Surface the failure where a TV user can see it: the
            // debug screen reads this (adb/logcat is not a given).
            lastError = Log.getStackTraceString(e)
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
        instance = null
        unregisterUnderlayCallback()
        tunnel?.let { runCatching { it.stop() }.onFailure { e -> Log.w(TAG, "stop", e) } }
        tunnel = null
        pfd?.let { runCatching { it.close() } }
        pfd = null
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
     * The underlay moved (wifi↔cellular, a new link): the resolvers
     * captured at establish() are stale, and so is what iroh knows
     * about our addresses — iroh-ffi's own network monitor is dead
     * inside an Android app (P2.4 finding .2), so this callback is the
     * redial trigger. The request's default NOT_VPN capability keeps
     * our own tun out of the updates.
     */
    private val underlayCallback = object : ConnectivityManager.NetworkCallback() {
        override fun onLinkPropertiesChanged(network: Network, lp: LinkProperties) {
            val dns = lp.dnsServers.filterIsInstance<Inet4Address>()
                .mapNotNull { it.hostAddress }
                .joinToString(",")
            if (dns.isNotEmpty()) tunnel?.setUpstreams(dns)
            val addrs = lp.linkAddresses.map { it.address }.filterIsInstance<Inet4Address>()
                .mapNotNull { it.hostAddress }
                .joinToString(",")
            tunnel?.networkChanged(addrs)
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

    /** Marks the resolver's underlay DNS sockets as VPN-bypassing. */
    private val protector = object : mobile.SocketProtector {
        override fun protect(fd: Int): Boolean = this@MeshVpnService.protect(fd)
    }

    /**
     * The active network's IPv4 resolvers (comma-separated) for the
     * non-mesh forwards. Must be read before establish(), so "active"
     * is the underlay. Empty ⇒ unknown in-zone names get NXDOMAIN and
     * out-of-zone queries are dropped, so the callback above matters.
     */
    private fun underlayDnsServers(): String {
        val cm = getSystemService(ConnectivityManager::class.java)
        val lp = cm.activeNetwork?.let { cm.getLinkProperties(it) } ?: return ""
        return lp.dnsServers.filterIsInstance<Inet4Address>()
            .mapNotNull { it.hostAddress }
            .joinToString(",")
    }

    /** The underlay's IPv4 addresses, for iroh to advertise as direct addrs. */
    private fun underlayAddresses(): String {
        val cm = getSystemService(ConnectivityManager::class.java)
        val lp = cm.activeNetwork?.let { cm.getLinkProperties(it) } ?: return ""
        return lp.linkAddresses.map { it.address }.filterIsInstance<Inet4Address>()
            .mapNotNull { it.hostAddress }.joinToString(",")
    }

    companion object {
        private const val TAG = "MeshVpnService"
        private const val CHANNEL = "mesh"
        private const val NOTIFICATION_ID = 1
        private const val MTU = 1280
        const val ACTION_STOP = "dev.marnyg.mesh.STOP"

        /** Read by MainActivity to render the toggle; volatile is enough
         *  for a UI hint (the source of truth is the service itself). */
        @Volatile
        var running = false
            private set

        /** The running service, for the screens' snapshots. */
        @Volatile
        private var instance: MeshVpnService? = null

        /** Stack trace of the last failed start (null after a
         *  successful one), for the debug screen. */
        @Volatile
        var lastError: String? = null
            private set

        /** Tunnel.StatusJSON, or null when no tunnel is running. */
        fun statusJson(): String? = instance?.tunnel?.let {
            try {
                it.statusJSON()
            } catch (e: Exception) {
                "{\"error\": \"${e.message}\"}"
            }
        }

        /** Tunnel.NamesJSON (the plane's name map), or null when not running. */
        fun namesJson(): String? = instance?.tunnel?.let {
            try {
                it.namesJSON()
            } catch (e: Exception) {
                null
            }
        }

        /** Where Go logs land: this session's while running, the
         *  previous session's after a stop (truncated at each start). */
        fun logFile(ctx: Context): File = File(ctx.cacheDir, "mesh.log")
    }
}
