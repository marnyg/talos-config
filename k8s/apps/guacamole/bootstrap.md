# guacamole — first-boot steps

Browser RDP to the win2k25 guest (`talos-config-ey3a`): open
<http://guac.gw.mesh.internal/> from a device in `group: admins` that
runs the mesh tun. Nothing else changes — `xfreerdp /v:rdp.gw.mesh.internal`
keeps working, the guest is untouched.

Two pods, no database:

| | |
|---|---|
| `guacd` | RDP client: dials `win2k25-rdp.vms.svc.cluster.local:3389` |
| `guacamole` | web app: login, connection list, the WebSocket tunnel |

## 1. Seal the real credentials (only manual step)

This commit carries **placeholder** values in `sealed-secret.yaml`
(`REPLACE-with-…`). Until it is re-sealed, the pods are healthy but the
Windows login fails. The plaintext template:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<user-mapping>
  <authorize username="marius" password="<guacamole login>">
    <connection name="win2k25">
      <protocol>rdp</protocol>
      <param name="hostname">win2k25-rdp.vms.svc.cluster.local</param>
      <param name="port">3389</param>
      <param name="username">Administrator</param>
      <param name="password">&lt;guest Administrator password&gt;</param>
      <param name="security">nla</param>
      <param name="ignore-cert">true</param>
      <param name="resize-method">display-update</param>
      <param name="color-depth">16</param>
      <param name="server-layout">en-us-qwerty</param>
      <param name="enable-audio">true</param>
      <param name="enable-wallpaper">false</param>
      <param name="enable-theming">false</param>
      <param name="enable-font-smoothing">false</param>
      <param name="enable-full-window-drag">false</param>
      <param name="enable-desktop-composition">false</param>
      <param name="enable-menu-animations">false</param>
    </connection>
  </authorize>
</user-mapping>
```

Seal it (the cert is public, no cluster access needed — the same recipe
the other apps use, e.g. `k8s/apps/forgejo/bootstrap.md`):

```sh
printf 'apiVersion: v1\nkind: Secret\nmetadata:\n  name: guacamole-user-mapping\n  namespace: guacamole\nstringData:\n  user-mapping.xml: |\n' > /tmp/secret.yaml
sed 's/^/    /' /tmp/user-mapping.xml >> /tmp/secret.yaml
kubeseal --cert talos/clusters/homelab/sealed-secrets.crt \
  --namespace guacamole --name guacamole-user-mapping \
  --format yaml < /tmp/secret.yaml > /tmp/sealed.yaml
# keep the header comment of k8s/apps/guacamole/sealed-secret.yaml,
# replace the document body with /tmp/sealed.yaml, commit, push.
rm -f /tmp/secret.yaml /tmp/user-mapping.xml
```

Then **recreate the pod** — `user-mapping.xml` is mounted with
`subPath`, and kubelet never updates a `subPath` mount:

```sh
kubectl -n guacamole delete pod -l app=guacamole
```

## 2. First login

`http://guac.gw.mesh.internal/` → nginx `app-root` sends `/` to
`/guacamole/`. The connection list shows `win2k25`; click it, and the
guest desktop appears fitted to the browser viewport.

- **Audio** needs one click on the page first (browser autoplay rules)
  — the RDP audio policies are already provisioned on the guest.
- **Sessions are cut after an hour.** The gateway bounds an admitted
  connection at `ConnMaxAge` (1 h); the page shows the disconnect and
  reconnecting is one click. Not an nginx timeout (those are 3600 s).
- **A halted VM shows as a connection error**, not a prompt — guacd
  just gets a refused socket. Until `vmpower` (`talos-config-f0jl`)
  exists, starting the VM is still a git edit of `runStrategy` in
  `k8s/apps/vms/win2k25-vm.yaml`.

## 3. When something is wrong

```sh
kubectl -n guacamole get pods
kubectl -n guacamole logs deploy/guacamole | tail -30
kubectl -n guacamole logs deploy/guacd     # per-session FreeRDP output
```

| symptom | cause |
|---|---|
| 403 / 401 from the browser | not an `admins` device on the mesh — the gate is the device cert, not this app |
| login page, then "Log in failed" | guest creds wrong. `guacd`'s log says which: `Authentication failure (invalid credentials?)` is the password, a TLS/NLA complaint is the security mode (then try `<param name="security">any</param>`) |
| tunnel drops every 60 s | the Ingress lost its `proxy-read/send-timeout` annotations (ingress.yaml) |
| page loads, tunnel never connects | it must be `http://`: ADR-0033's `:443` device front has no Upgrade path, so `wss://` dies there |
| sluggish desktop | expected — guacd software-decodes. **`color-depth` is a no-op**: the RDP graphics pipeline forces 32 bpp whatever you ask for (`WARNING: Ignoring requested color depth of 16 bpp` in the `guacd` log, 2026-10-22). Cheaper levers: disable the graphics pipeline, or use the native client over `rdp.gw.mesh.internal:3389` |

Two things the first real session proved (2026-10-22): the WebSocket tunnel
survives the whole path (browser → gateway → nginx → Tomcat → guacd), and
`security: nla` negotiates with Server 2025 as-is — no `any` fallback needed.
