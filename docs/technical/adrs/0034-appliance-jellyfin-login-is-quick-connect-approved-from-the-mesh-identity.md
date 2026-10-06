# ADR-0034: An appliance logs into Jellyfin by Quick Connect, approved from its mesh identity

- Status: Accepted — built and confirmed 2026-10-06: the Shield's
  Jellyfin app, pointed at `jellyfin.gw` (port 80), pressed Quick
  Connect and was signed in as user `tv` (non-admin) with no further
  action; playback over the ingress door confirmed. One fix on the
  way: strip `Accept-Encoding` on the Initiate, or the hook reads gzip
- Date: 2026-10-06
- Related: ADR-0032 (the gateway-signed identity token; this is its
  second verifier), ADR-0017 (facets), ADR-0013 (the TV as a mesh
  client); spike `talos-config-95la`, memo
  `docs/spikes/auth-mesh-identity.md` ("Where it cannot work", open
  question 5); spike `talos-config-7ymy` (person binding)

## Context and Problem Statement

A TV app cannot do a wallet sign-in: no injected provider in its
webview, and the SIWE page is useless to it. Until now the TV was
Quick-Connected by hand from inside the cluster — as the local
`admin`, so the appliance held an administrator token (notes.md,
2026-09-20). ADR-0032 settled the app seam for browsers (the gateway
signs who the caller is into `X-Mesh-Token`; the bridge decides) and
explicitly left the appliance out: the TV used the raw `jellyfin`
splice, which carries no HTTP, and even an HTTP hop "would not help"
because Jellyfin clients need a Jellyfin access token, which no header
produces.

The question `95la` left open: which Jellyfin principal is an
appliance, and how does it get its token without a human or a
password on the device?

## Decision Drivers

- The mesh already knows who the TV is (member cert: key, name,
  group). Making the owner re-state that by typing a password on a
  remote, or approving a code by hand, duplicates a fact the system
  holds.
- ADR-0032 ruling 2: device login mints the *device*, never a mapped
  person. A shared appliance's watch state should be its own.
- An appliance must never hold an administrator token.
- Invariants 1–2 are untouched either way: Jellyfin users are
  data-plane state (the app volume, ADR-0011), not identity state;
  the authority decision still reads only the signed token against a
  key pinned in git.
- Structural trade-off (invariants.md): capability discipline ends
  one signed hop past the gateway. A second verifier of that hop is
  allowed; a third hop of ambient trust is not.

## Considered Options

- **A — Quick Connect by hand, as today.** No code; the appliance
  becomes whoever approves (today `admin`). Fails the admin-token
  driver and the device-principal driver.
- **B — Configurator-minted per-device accounts, password typed once
  on the TV.** ~30 lines of shell; right principal; password on the
  remote and in a sealed secret; the mesh identity plays no part.
- **C — A Jellyfin `IAuthenticationProvider` plugin trusting a
  header.** C#, and the interface sees username/password only, not
  the request — it cannot read the token. Ruled out in the memo.
- **D — Quick Connect, approved automatically from the token.** Quick
  Connect's protocol has exactly one human step: someone signed in
  calls `POST /QuickConnect/Authorize?code=…&userId=…` (admins may
  name another user). A proxy in front of Jellyfin that sees the
  gateway's token on `POST /QuickConnect/Initiate` can take that step
  itself, for the device's own user, before the response returns. The
  device presses Quick Connect and is in. Needs the HTTP door, so the
  appliance leaves the raw splice for `jellyfin.gw:80`.

## Decision Outcome

**D.** `config-server/jellyfinqc` is a reverse proxy sidecar in the
Jellyfin pod and the backend of the `jellyfin.gw` Ingress. It
forwards everything (streams unbuffered); on a successful
`Initiate` whose request carried a valid `X-Mesh-Token` — verified
exactly as the bridge does, against the gateway id pinned in the
manifest — from a device in the `media` group, it ensures a Jellyfin
user named after the device (created non-administrator, hidden from
the login screen, all libraries, a random password it forgets) and
authorizes the code for that user with the admin credential the pod
already holds. It also keeps `QuickConnectAvailable` on, which had
been a hand-made setting.

Rulings:

1. **The appliance's Jellyfin principal is the device**, named by the
   member cert's `name` — so a re-keyed or re-enrolled device keeps
   its watch state (invariant 1's "a role owns a name"). Two devices
   approved under one name share one user, as they share one DNS
   label.
2. **Never onto an administrator.** If the device's name is an
   existing admin user, nothing is approved. An appliance never holds
   an admin token, whoever named it.
3. **Only the `media` group is logged in automatically.** An
   `admins` device (the owner's phone) keeps manual Quick Connect, so
   the owner can still approve it as the person from a wallet session
   — the person-side answer until `7ymy` lands.
4. **The token is a shortcut, never a lockout.** No token, a foreign
   or stale token, the wrong group: the `Initiate` is still proxied
   and Quick Connect waits for a human, Jellyfin's own behaviour. A
   pod forging `X-Mesh-*` toward ingress-nginx gets exactly that.
5. The appliances move to the HTTP door and **the raw `jellyfin`
   facet is retired** (same day, once the TV was seen on the new
   path): one door, no path that bypasses the identity seam. Gone from
   `policy.facets`, the recipe, the gateway's flags and the bridge's
   `:8096` redirect origin; the Service keeps `:8096` for in-cluster
   callers (seerr).

### Consequences

- Good: zero credentials on the appliance, zero owner action per
  device, the device principal falls out of the mesh identity, and
  the hand-made Quick Connect toggle is now declared.
- Good: the gateway stays network-layer; the app-specific knowledge
  lives beside the app, as the bridge does for OIDC. `meshtoken` has
  two consumers now, which is what a seam is for.
- Cost: the TV's stream crosses ingress-nginx and the sidecar instead
  of a splice. The browser already streams that way; the sidecar is a
  Go `ReverseProxy` with `FlushInterval -1`.
- Cost: a re-keyed gateway is now **two** pin lines (bridge and
  sidecar); until both move, the TV's Quick Connect falls back to
  manual rather than failing.
- When `7ymy` binds a device to a person in the cert, this sidecar
  is where it lands for Jellyfin: approve as the person's user
  instead of the device's. No new seam.
