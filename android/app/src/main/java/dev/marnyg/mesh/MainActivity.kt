package dev.marnyg.mesh

import android.app.Activity
import android.content.Intent
import android.graphics.BitmapFactory
import android.net.VpnService
import android.os.Bundle
import android.util.Base64
import android.view.View
import android.widget.ArrayAdapter
import android.widget.Button
import android.widget.EditText
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ListView
import android.widget.TextView
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import mobile.Mobile
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

/**
 * The whole UI: one activity, two states.
 *
 *  - Not enrolled: RFC 8628 device flow. The app generates its keypair
 *    (identity never leaves the device), shows the hub's QR + user
 *    code; the owner scans with the phone, signs with the wallet at
 *    /status, and the app polls /token until the config arrives.
 *  - Enrolled: connect toggle (VpnService) + the mesh host list from
 *    the hub's /hosts, Tailscale-style.
 */
class MainActivity : Activity() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)

    private lateinit var enrollGroup: LinearLayout
    private lateinit var mainGroup: LinearLayout
    private lateinit var hubInput: EditText
    private lateinit var nameInput: EditText
    private lateinit var enrollButton: Button
    private lateinit var qrImage: ImageView
    private lateinit var userCode: TextView
    private lateinit var enrollStatus: TextView
    private lateinit var toggleButton: Button
    private lateinit var refreshButton: Button
    private lateinit var debugButton: Button
    private lateinit var connStatus: TextView
    private lateinit var hostsList: ListView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        enrollGroup = findViewById(R.id.enroll_group)
        mainGroup = findViewById(R.id.main_group)
        hubInput = findViewById(R.id.hub_input)
        nameInput = findViewById(R.id.name_input)
        enrollButton = findViewById(R.id.enroll_button)
        qrImage = findViewById(R.id.qr_image)
        userCode = findViewById(R.id.user_code)
        enrollStatus = findViewById(R.id.enroll_status)
        toggleButton = findViewById(R.id.toggle_button)
        refreshButton = findViewById(R.id.refresh_button)
        debugButton = findViewById(R.id.debug_button)
        connStatus = findViewById(R.id.conn_status)
        hostsList = findViewById(R.id.hosts_list)

        enrollButton.setOnClickListener { startEnroll() }
        toggleButton.setOnClickListener { toggleVpn() }
        refreshButton.setOnClickListener { refreshHosts() }
        debugButton.setOnClickListener { startActivity(Intent(this, DebugActivity::class.java)) }
    }

    override fun onResume() {
        super.onResume()
        render()
        if (Store.config(this) != null && MeshVpnService.running) refreshHosts()
    }

    override fun onDestroy() {
        scope.cancel()
        super.onDestroy()
    }

    private fun render() {
        val enrolled = Store.config(this) != null
        enrollGroup.visibility = if (enrolled) View.GONE else View.VISIBLE
        mainGroup.visibility = if (enrolled) View.VISIBLE else View.GONE
        if (enrolled) {
            connStatus.text = getString(
                if (MeshVpnService.running) R.string.status_connected
                else R.string.status_disconnected
            )
            toggleButton.text = getString(
                if (MeshVpnService.running) R.string.disconnect else R.string.connect
            )
        }
    }

    // -- Enrollment ------------------------------------------------------

    private fun startEnroll() {
        val hub = hubInput.text.toString().trim().trimEnd('/')
        val name = nameInput.text.toString().trim().ifEmpty { "tv" }
        enrollButton.isEnabled = false
        enrollStatus.text = getString(R.string.starting_enrollment)
        scope.launch {
            try {
                val priv = Store.privKey(this@MainActivity) ?: run {
                    // Device-born identity: generated here, persisted here,
                    // never sent anywhere. Re-enrollments reuse it so the
                    // device's mesh address never moves.
                    val kp = JSONObject(withContext(Dispatchers.IO) { Mobile.generateKeypair() })
                    val hex = kp.getString("privHex")
                    Store.setPrivKey(this@MainActivity, hex)
                    hex
                }
                val start = JSONObject(withContext(Dispatchers.IO) {
                    Mobile.startEnroll(hub, priv, name, "media")
                })
                val png = Base64.decode(start.getString("qr_png_base64"), Base64.DEFAULT)
                qrImage.setImageBitmap(BitmapFactory.decodeByteArray(png, 0, png.size))
                userCode.text = start.getString("user_code")
                enrollStatus.text = getString(R.string.scan_prompt)

                val deviceCode = start.getString("device_code")
                var interval = start.optInt("interval", 5)
                while (true) {
                    delay(interval * 1000L)
                    val poll = JSONObject(withContext(Dispatchers.IO) {
                        Mobile.pollEnroll(hub, deviceCode)
                    })
                    when (poll.getString("status")) {
                        "pending" -> {}
                        "slow_down" -> interval += 5
                        "ok" -> {
                            val cfg = withContext(Dispatchers.IO) {
                                Mobile.fetchConfig(hub, poll.getString("accessToken"), priv)
                            }
                            Store.setConfig(this@MainActivity, cfg)
                            qrImage.setImageDrawable(null)
                            render()
                            return@launch
                        }
                        "denied" -> {
                            enrollStatus.text = getString(R.string.enroll_denied)
                            return@launch
                        }
                        "expired" -> {
                            enrollStatus.text = getString(R.string.enroll_expired)
                            return@launch
                        }
                    }
                }
            } catch (e: Exception) {
                enrollStatus.text = getString(R.string.error_fmt, e.message ?: e.toString())
            } finally {
                enrollButton.isEnabled = true
            }
        }
    }

    // -- Tunnel ----------------------------------------------------------

    private fun toggleVpn() {
        if (MeshVpnService.running) {
            startService(
                Intent(this, MeshVpnService::class.java).setAction(MeshVpnService.ACTION_STOP)
            )
            // The service flips `running` synchronously in onStartCommand;
            // give it a beat, then re-render.
            scope.launch { delay(300); render() }
            return
        }
        val consent = VpnService.prepare(this)
        if (consent != null) {
            startActivityForResult(consent, REQUEST_VPN)
        } else {
            onActivityResult(REQUEST_VPN, RESULT_OK, null)
        }
    }

    @Deprecated("Deprecated in Java")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode != REQUEST_VPN || resultCode != RESULT_OK) return
        startForegroundService(Intent(this, MeshVpnService::class.java))
        scope.launch {
            delay(500)
            render()
            refreshHosts()
        }
    }

    // -- Host list -------------------------------------------------------

    private fun refreshHosts() {
        val cfg = Store.config(this) ?: return
        if (!MeshVpnService.running) {
            // Without the tun route the fetch would leave via wifi and
            // time out with a misleading "hub unreachable".
            connStatus.text = getString(R.string.status_disconnected)
            return
        }
        scope.launch {
            try {
                val rows = withContext(Dispatchers.IO) {
                    val hubIP = JSONObject(Mobile.configInfo(cfg)).getString("hubIP")
                    val conn = URL("http://$hubIP/hosts").openConnection() as HttpURLConnection
                    conn.connectTimeout = 4000
                    conn.readTimeout = 4000
                    try {
                        val arr = JSONObject(conn.inputStream.bufferedReader().readText())
                            .getJSONArray("hosts")
                        (0 until arr.length()).map { i ->
                            val h = arr.getJSONObject(i)
                            val dot = if (h.getBoolean("online")) "\u25CF" else "\u25CB"
                            "$dot  ${h.getString("name")}   ${h.getString("ip")}   (${h.getString("kind")})"
                        }
                    } finally {
                        conn.disconnect()
                    }
                }
                hostsList.adapter =
                    ArrayAdapter(this@MainActivity, android.R.layout.simple_list_item_1, rows)
            } catch (e: Exception) {
                connStatus.text = getString(R.string.hub_unreachable_fmt, e.message ?: "")
            }
        }
    }

    companion object {
        private const val REQUEST_VPN = 1
    }
}
