# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-03 (evening; the storage session the previous handoff dated
2026-10-04 was earlier the same day): **the gateway panic is fixed and
rolled.**

- **`vzbf`** (`c4a6414`, `iroh-transport/streamfacet.go`): `Raw.Read`
  after `Close` called the FFI on a destroyed `RecvStream`; httputil's
  WebSocket copier does exactly that once the other half closes, so
  every Sonarr/Radarr SignalR session could take the gateway down.
  Every FFI call now passes a per-direction in-flight gate under `mu`;
  after `Close`, Read/Write/CloseWrite return `net.ErrClosed`, and the
  handles are destroyed by whichever is later — `Close` or the last
  in-flight call. Regression test in `TestStreamFacet`.
- **Found on the way**: the old "Close unblocks a blocked Read" claim
  was never true. iroh-ffi holds one tokio `Mutex` across `read()` and
  `stop()`, and the Go bindgen exposes no future cancel, so `Close`
  concurrent with a blocked `Read` *deadlocked* on `Stop`. `Close` now
  skips the abort on a direction with a call in flight and still resets
  the idle send side — the peer ending its stream is what returns our
  `Read`. Thread `vh6e` holds the upstream fix.
- vendorHash bumped in `config-server` (`a6e074c`) and `actors/`
  (`21badd6`). Image `ghcr.io/marnyg/gateway:21badd6` pushed, pinned
  `86cd681`; ArgoCD needed a refresh nudge; pod up with 0 restarts and
  SignalR negotiated (the old pod had 13).
- `scripts/ghcr-push.sh` resolves credentials before the build and
  falls back to `~/.docker/config.json` (it died with exit 127 on
  linux after the full build). `facethttp.Conn`'s deadline comment now
  says the truth: `ReadHeaderTimeout` does not bite over facets.

## Loose threads

- **`vzbf`** is `in_progress` pending a day of SignalR traffic with 0
  restarts; close it then.
- **`vh6e`** (P3 thread): a `Raw` whose peer never ends leaks its Read
  goroutine until `DefaultConnMaxAge` (1 h); facethttp deadlines are
  no-ops for the same reason. Needs an iroh-ffi patch (lock per call
  or expose cancel) + Go regen; `zbgk` is the other patch candidate.
- **ADR-0029** still *Proposed*. **`jx78`**: default `longhorn` class
  not fenced to `nvme`. `cnb5`: faulted volumes on `/status` left.
- Old docker host: media containers stopped, not removed; its disk is
  the library's only second copy (`notes.md`).
- `installMAC` → `spvd`; agent fixes → `9af0` (unchanged).

## Suggested next steps

- Review ADR-0029 → Accepted; then `jx78` (fence the default class).
- `cnb5` remainder: faulted Longhorn volumes on `/status`.
- Owner `todo` leftovers: seerr/syncthing/sillytavern, old docker host
  retirement, Windows PC as compute node.
