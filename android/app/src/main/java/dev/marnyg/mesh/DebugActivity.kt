package dev.marnyg.mesh

import android.app.Activity
import android.os.Bundle
import android.os.SystemClock
import android.widget.Button
import android.widget.EditText
import android.widget.TextView
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import mobile.Mobile
import org.json.JSONObject
import java.io.RandomAccessFile
import java.net.InetAddress

/**
 * The introspection screen (D-pad friendly): the split-DNS shim's
 * state from Tunnel.DebugJSON (addressing, live upstreams, counters,
 * recent per-query decisions) plus the tail of this session's nebula
 * log. "Test DNS" resolves one mesh name and one public name through
 * the system resolver — with the tunnel up that traverses the tun's
 * magic resolver, exercising the exact mesh-vs-underlay split real
 * apps hit; both lookups then show up in the event ring.
 */
class DebugActivity : Activity() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)

    private lateinit var lookupInput: EditText
    private lateinit var testStatus: TextView
    private lateinit var debugText: TextView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_debug)
        lookupInput = findViewById(R.id.lookup_input)
        testStatus = findViewById(R.id.test_status)
        debugText = findViewById(R.id.debug_text)
        findViewById<Button>(R.id.debug_refresh_button).setOnClickListener { refresh() }
        findViewById<Button>(R.id.test_dns_button).setOnClickListener { testDns() }
    }

    override fun onResume() {
        super.onResume()
        refresh()
    }

    override fun onDestroy() {
        scope.cancel()
        super.onDestroy()
    }

    private fun refresh() {
        scope.launch {
            debugText.text = withContext(Dispatchers.IO) { buildReport() }
        }
    }

    private fun buildReport(): String = buildString {
        append("== DNS shim (Tunnel.DebugJSON) ==\n")
        val dbg = MeshVpnService.debugJson()
        append(
            if (dbg == null) getString(R.string.debug_not_running)
            else try {
                JSONObject(dbg).toString(2)
            } catch (e: Exception) {
                dbg // show raw rather than nothing
            }
        )
        append("\n\n== last tunnel start error ==\n")
        append(MeshVpnService.lastError ?: getString(R.string.debug_no_error))
        append("\n\n== nebula log (tail) ==\n")
        append(logTail())
    }

    /** Last TAIL_BYTES of the session log, trimmed to a line boundary. */
    private fun logTail(): String {
        val f = MeshVpnService.logFile(this)
        if (!f.exists() || f.length() == 0L) return getString(R.string.debug_no_log)
        RandomAccessFile(f, "r").use { raf ->
            val start = maxOf(0L, raf.length() - TAIL_BYTES)
            raf.seek(start)
            val buf = ByteArray((raf.length() - start).toInt())
            raf.readFully(buf)
            var s = String(buf, Charsets.UTF_8)
            if (start > 0) s = "…" + s.substringAfter('\n')
            return s
        }
    }

    // -- DNS self-test -----------------------------------------------

    private fun testDns() {
        val extra = lookupInput.text.toString().trim()
        testStatus.text = getString(R.string.dns_test_running)
        scope.launch {
            val report = withContext(Dispatchers.IO) {
                // "hub" is nebderive.HubName — always in the mesh zone,
                // always the resolver's own address, so it isolates DNS
                // from whichever host the user is actually chasing.
                val zone = try {
                    val cfg = Store.config(this@DebugActivity) ?: throw IllegalStateException()
                    JSONObject(Mobile.configInfo(cfg)).getString("dnsZone")
                } catch (e: Exception) {
                    "mesh.internal"
                }
                (listOf("hub.$zone", "example.com") + listOf(extra).filter { it.isNotEmpty() })
                    .joinToString("\n") { resolveOne(it) }
            }
            testStatus.text = report
            refresh() // the lookups just landed in the shim's event ring
        }
    }

    private fun resolveOne(name: String): String = try {
        val t0 = SystemClock.elapsedRealtime()
        val addrs = InetAddress.getAllByName(name).joinToString(", ") { it.hostAddress ?: "?" }
        "\u2713 $name \u2192 $addrs (${SystemClock.elapsedRealtime() - t0} ms)"
    } catch (e: Exception) {
        "\u2717 $name \u2192 ${e.message}"
    }

    companion object {
        private const val TAIL_BYTES = 32_000L
    }
}
