package dev.marnyg.mesh

import android.app.Activity
import android.app.AlertDialog
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
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import mobile.Mobile
import org.json.JSONArray
import org.json.JSONObject

/**
 * The whole UI: one activity, two states.
 *
 *  - Not enrolled: the headless device flow, run by Go
 *    (Mobile.enroll): the app's NodeId is minted on-device, the hub
 *    answers with a QR + user code, the owner scans with the phone
 *    and signs with the wallet at /status; Go polls until the Kit
 *    arrives and persists it.
 *  - Enrolled: connect toggle (VpnService) + the plane's name map as
 *    this member sees it (Tunnel.NamesJSON), Tailscale-style.
 */
class MainActivity : Activity() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)
    private var enrollJob: Job? = null

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

        hubInput.setText(Store.hub(this))
        Store.name(this)?.let { nameInput.setText(it) }
        enrollButton.setOnClickListener { startEnroll() }
        toggleButton.setOnClickListener { toggleVpn() }
        refreshButton.setOnClickListener { refreshNames() }
        debugButton.setOnClickListener { startActivity(Intent(this, DebugActivity::class.java)) }
    }

    override fun onResume() {
        super.onResume()
        render()
        if (MeshVpnService.running) refreshNames()
    }

    override fun onDestroy() {
        Mobile.cancelEnroll()
        scope.cancel()
        super.onDestroy()
    }

    private fun enrolled(): Boolean = Mobile.enrolled(Store.stateDir(this).absolutePath)

    private fun render() {
        val enrolled = enrolled()
        enrollGroup.visibility = if (enrolled) View.GONE else View.VISIBLE
        mainGroup.visibility = if (enrolled) View.VISIBLE else View.GONE
        if (enrolled) {
            val member = try {
                val m = JSONObject(Mobile.memberJSON(Store.stateDir(this).absolutePath))
                " — ${m.getString("name")} ${m.optJSONArray("groups")?.join(",")?.replace("\"", "") ?: ""}"
            } catch (e: Exception) {
                ""
            }
            connStatus.text = getString(
                if (MeshVpnService.running) R.string.status_connected
                else R.string.status_disconnected
            ) + member
            toggleButton.text = getString(
                if (MeshVpnService.running) R.string.disconnect else R.string.connect
            )
        }
    }

    // -- Enrollment ------------------------------------------------------

    private fun startEnroll() {
        val hub = hubInput.text.toString().trim().trimEnd('/')
        val name = nameInput.text.toString().trim().ifEmpty { "tv" }
        Store.setHub(this, hub)
        Store.setName(this, name)
        enrollButton.isEnabled = false
        enrollStatus.text = getString(R.string.starting_enrollment)
        val stateDir = Store.stateDir(this).absolutePath
        val listener = object : mobile.EnrollListener {
            // Called from a Go thread once the hub has answered.
            override fun show(userCode: String, approveURL: String, qrPNGBase64: String) {
                runOnUiThread {
                    if (qrPNGBase64.isNotEmpty()) {
                        val png = Base64.decode(qrPNGBase64, Base64.DEFAULT)
                        qrImage.setImageBitmap(BitmapFactory.decodeByteArray(png, 0, png.size))
                    }
                    this@MainActivity.userCode.text = userCode
                    enrollStatus.text = getString(R.string.scan_prompt)
                }
            }
        }
        enrollJob?.cancel()
        enrollJob = scope.launch {
            try {
                // Blocks until signed / denied / expired / cancelled.
                withContext(Dispatchers.IO) { Mobile.enroll(stateDir, hub, name, "media", listener) }
                qrImage.setImageDrawable(null)
                userCode.text = ""
                render()
            } catch (e: Exception) {
                val msg = e.message ?: e.toString()
                enrollStatus.text = when {
                    Mobile.isCancelled(msg) -> ""
                    msg.contains("denied") -> getString(R.string.enroll_denied)
                    msg.contains("expired") -> getString(R.string.enroll_expired)
                    else -> getString(R.string.error_fmt, msg)
                }
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
            // First time (or consent revoked): Android's dialog follows;
            // say what it costs first — one VpnService per device.
            AlertDialog.Builder(this)
                .setMessage(R.string.vpn_slot_notice)
                .setPositiveButton(android.R.string.ok) { _, _ -> startActivityForResult(consent, REQUEST_VPN) }
                .setNegativeButton(android.R.string.cancel, null)
                .show()
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
            refreshNames()
        }
    }

    // -- Name list -------------------------------------------------------

    /** The plane as this member sees it: the last beat's name map. */
    private fun refreshNames() {
        if (!MeshVpnService.running) {
            connStatus.text = getString(R.string.status_disconnected)
            return
        }
        scope.launch {
            val rows = withContext(Dispatchers.IO) {
                val raw = MeshVpnService.namesJson() ?: return@withContext emptyList()
                val arr = JSONArray(raw)
                (0 until arr.length()).map { i ->
                    val h = arr.getJSONObject(i)
                    val dot = if (h.getBoolean("online")) "\u25CF" else "\u25CB"
                    "$dot  ${h.getString("name")}.${Mobile.Zone}   (${h.getString("kind")})"
                }
            }
            if (rows.isEmpty()) {
                MeshVpnService.statusJson()?.let {
                    val st = JSONObject(it)
                    if (st.optString("fatal").isNotEmpty()) {
                        connStatus.text = getString(R.string.error_fmt, st.getString("fatal"))
                    } else if (st.optLong("beats") == 0L) {
                        connStatus.text = getString(R.string.status_waiting_beat)
                    }
                }
            }
            hostsList.adapter =
                ArrayAdapter(this@MainActivity, android.R.layout.simple_list_item_1, rows)
        }
    }

    companion object {
        private const val REQUEST_VPN = 1
    }
}
