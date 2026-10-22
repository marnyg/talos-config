# guacamole — first-boot steps

Browser RDP to the win2k25 guest (`talos-config-ey3a`): open
<http://guac.gw.mesh.internal/> from a device in `group: admins` that
runs the mesh tun. Nothing else changes — `xfreerdp /v:rdp.gw.mesh.internal`
keeps working, the guest is untouched.

Two pods, no database, no Guacamole login (ADR-0045):

| | |
|---|---|
| `guacd` | RDP client: dials `win2k25-rdp.vms.svc.cluster.local:3389` |
| `guacamole` | web app (`guacamole` container): connection list, the WebSocket tunnel; its only auth extension is `guacamole-auth-json` |
| ↳ `front` sidecar | `guacjson` (config-server/guacjson): the Service lands here; it signs the device into the webapp from the gateway's X-Mesh-Token |

## 1. Reseal the credentials (the only manual step)

`sealed-secret.yaml` holds `guacamole-auth` with two keys:
`json-secret-key` (16 random bytes, hex; both containers) and
`guest-password` (the guest's Administrator password; sidecar only).
To rotate either:

```sh
KEY=$(openssl rand -hex 16)            # or keep the current one
kubectl create secret generic guacamole-auth -n guacamole --dry-run=client -o yaml \
  --from-literal=json-secret-key="$KEY" --from-literal=guest-password="$PW" \
| kubeseal --cert talos/clusters/homelab/sealed-secrets.crt --format yaml
# keep the header comment of sealed-secret.yaml, replace the document
# body, commit, push. Env from a Secret needs a rollout:
kubectl -n guacamole rollout restart deploy/guacamole
```

The RDP parameters (host, port, user, the visual-effect switches) are
`-param` args on the sidecar in `deployment.yaml` — change them there.

## 2. First login

`http://guac.gw.mesh.internal/` → nginx `app-root` sends `/` to
`/guacamole/`. No form: the client's own `POST api/tokens` carries no
credentials, the sidecar verifies the gateway token, requires `admins`,
and adds the extension's `data` blob — the device name is the Guacamole
user, the one connection is `win2k25`. Click it and the guest desktop
appears fitted to the browser viewport. The sidecar logs
`device … logged in as guacamole user "<name>"` per login.

- **Audio** needs one click on the page first (browser autoplay rules)
  — the RDP audio policies are already provisioned on the guest.
- **Sessions are cut after an hour.** The gateway bounds an admitted
  connection at `ConnMaxAge` (1 h); the page shows the disconnect and
  reconnecting is one click. Not an nginx timeout (those are 3600 s).
- **A halted VM shows as a connection error**, not a prompt — guacd
  just gets a refused socket. Start it from
  <http://win.gw.mesh.internal/> (`vmpower`, `talos-config-f0jl`) and
  wait for `Running`.

## 3. When something is wrong

```sh
kubectl -n guacamole get pods
kubectl -n guacamole logs deploy/guacamole -c guacamole | tail -30
kubectl -n guacamole logs deploy/guacamole -c front       # who was signed in, or why not
kubectl -n guacamole logs deploy/guacd     # per-session FreeRDP output
```

| symptom | cause |
|---|---|
| 403 / 401 from the browser | not an `admins` device on the mesh — the gate is the device cert, not this app |
| **a Guacamole login form** | the sidecar did not mint: `kubectl -n guacamole logs deploy/guacamole -c front` says why (no gateway token = request did not come through the gateway; token refused = the pinned `-gateway` id is stale, compare siwe-oidc's; "not admins" = the device's groups). The form itself authenticates nobody — there is no user list |
| connection shows, then "Log in failed" / invalid credentials | guest password wrong (`guest-password` in the secret) or the extension key mismatched between the two containers (`JSON_SECRET_KEY` vs `GUACJSON_SECRET_KEY` — same secret key, so only after a partial rollout). `guacd`'s log says which: `Authentication failure (invalid credentials?)` is the password, a TLS/NLA complaint is the security mode (then add `-param=security=any`) |
| tunnel drops every 60 s | the Ingress lost its `proxy-read/send-timeout` annotations (ingress.yaml) |
| "Address banned" | should not happen: `BAN_ENABLED=false` (deployment.yaml), and there is no password to fail any more. If it appears, the env var was lost |
| page loads, tunnel never connects | it must be `http://`: ADR-0033's `:443` device front has no Upgrade path, so `wss://` dies there |
| sluggish desktop | expected — guacd software-decodes. **`color-depth` is a no-op**: the RDP graphics pipeline forces 32 bpp whatever you ask for (`WARNING: Ignoring requested color depth of 16 bpp` in the `guacd` log, 2026-10-22). Cheaper levers: disable the graphics pipeline, or use the native client over `rdp.gw.mesh.internal:3389` |

### The lockout trap (cost 20 minutes on 2026-10-22)

Guacamole's page auto-reconnects every ~13 s. **Every reconnect is an RDP
logon attempt**, and Windows Server 2025 ships with account lockout
enabled (threshold 10, duration ~10 min). So a wrong password behind an
open tab locks `Administrator` out in about two and a half minutes — and
a *locked* account answers `Access denied by server (account locked or
disabled?)` for **every** later attempt, correct password included. The
error moves from "invalid credentials" to "access denied" and stays
there, which reads like a permissions problem and is not.

- Queue of failures, in the `guacd` log: 10× `Authentication failure`
  ⇒ then `Access denied by server`. That transition *is* the lockout.
- Response: close the tab, wait ~10 min, load it fresh, try **once**.
- `xfreerdp` hits the same lockout, so "it works from the laptop" stops
  being true at the same moment.
- Durability of the trap is a policy choice: raising the threshold (or
  shortening the duration) belongs in `win2k25-provision.yaml`, not in
  Guacamole — nothing on the client side can stop the loop.

Two things the first real session proved (2026-10-22): the WebSocket tunnel
survives the whole path (browser → gateway → nginx → Tomcat → guacd), and
`security: nla` negotiates with Server 2025 as-is — no `any` fallback needed.
