package dev.marnyg.p0mesh

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.text.InputType
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import org.json.JSONObject

/**
 * One screen: relay URL, peer NodeId, Start / Stop, live stats. Inputs are
 * remembered in SharedPreferences so a TV only has to type them once.
 * Layout is built in code — no resources to maintain for a spike.
 */
class MainActivity : Activity() {
    private lateinit var relay: EditText
    private lateinit var peer: EditText
    private lateinit var stats: TextView
    private val handler = Handler(Looper.getMainLooper())
    private val prefs by lazy { getSharedPreferences("p0mesh", Context.MODE_PRIVATE) }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val root = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; setPadding(32, 32, 32, 32) }
        relay = EditText(this).apply {
            hint = "relay URL"; inputType = InputType.TYPE_TEXT_VARIATION_URI
            setText(prefs.getString("relay", "https://marnyg-iroh-relay-spike.fly.dev"))
        }
        peer = EditText(this).apply {
            hint = "peer NodeId (hex)"; inputType = InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
            setText(prefs.getString("peer", ""))
        }
        val row = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
        val start = Button(this).apply { text = "Start"; setOnClickListener { start() } }
        val stop = Button(this).apply { text = "Stop"; setOnClickListener { stop() } }
        row.addView(start); row.addView(stop)
        stats = TextView(this).apply { typeface = android.graphics.Typeface.MONOSPACE; textSize = 13f }
        root.addView(TextView(this).apply { text = "p0mesh — Mesh v3 P0.2 spike"; textSize = 20f })
        root.addView(relay); root.addView(peer); root.addView(row)
        root.addView(ScrollView(this).apply { addView(stats) })
        setContentView(root)
        start.requestFocus()
    }

    override fun onResume() { super.onResume(); tick() }
    override fun onPause() { super.onPause(); handler.removeCallbacksAndMessages(null) }

    private fun start() {
        prefs.edit().putString("relay", relay.text.toString().trim()).putString("peer", peer.text.toString().trim()).apply()
        val consent = VpnService.prepare(this)
        if (consent != null) startActivityForResult(consent, 1) else launch()
    }

    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == 1 && resultCode == RESULT_OK) launch()
    }

    private fun launch() {
        P0VpnService.lastError = null
        startForegroundService(
            Intent(this, P0VpnService::class.java)
                .putExtra(P0VpnService.EXTRA_RELAY, relay.text.toString().trim())
                .putExtra(P0VpnService.EXTRA_PEER, peer.text.toString().trim())
        )
    }

    private fun stop() {
        startService(Intent(this, P0VpnService::class.java).setAction(P0VpnService.ACTION_STOP))
    }

    private var lastIn = 0L
    private var lastOut = 0L
    private var lastT = 0L

    private fun tick() {
        val json = P0VpnService.statsJson()
        stats.text = when {
            json != null -> render(JSONObject(json))
            P0VpnService.lastError != null -> "error: ${P0VpnService.lastError}"
            else -> "tunnel down"
        }
        handler.postDelayed({ tick() }, 1000)
    }

    /** Counters plus a 1 s rate — the ≥ 80 Mbps question, read off the screen. */
    private fun render(j: JSONObject): String {
        val now = System.currentTimeMillis()
        val inB = j.getLong("bytesIn"); val outB = j.getLong("bytesOut")
        var rate = ""
        if (lastT != 0L && now > lastT) {
            val mbpsIn = (inB - lastIn) * 8.0 / 1e6 / ((now - lastT) / 1000.0)
            val mbpsOut = (outB - lastOut) * 8.0 / 1e6 / ((now - lastT) / 1000.0)
            rate = "rate     in %.1f Mbps  out %.1f Mbps\n".format(mbpsIn, mbpsOut)
        }
        lastIn = inB; lastOut = outB; lastT = now
        val sb = StringBuilder()
        sb.append("id       ${j.getString("id")}\n")
        sb.append("peer     ${j.getString("peer")}\n")
        sb.append("up       ${j.getLong("uptimeS")} s   connected=${j.getBoolean("connected")} redials=${j.getLong("redials")}\n")
        sb.append("paths    ${j.optJSONArray("paths")?.join(" ") ?: "-"}\n")
        sb.append(rate)
        sb.append("bytes    in %.1f MB  out %.1f MB\n".format(inB / 1e6, outB / 1e6))
        sb.append("flows    total ${j.getLong("flows")}  open ${j.getLong("flowsOpen")}  errors ${j.getLong("flowErrors")}\n")
        sb.append("dns      mesh ${j.getLong("dnsMesh")}  underlay ${j.getLong("dnsUnderlay")}  fail ${j.getLong("dnsFail")}\n")
        val names = j.optJSONArray("names")
        if (names != null) for (i in 0 until names.length()) sb.append("  ${names.getString(i)}\n")
        return sb.toString()
    }
}
