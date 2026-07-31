# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Growing the cluster past one node, and rebuilding storage for a
multi-node world. w1 (Alienware x15) is a Ready worker; its repartition
into a 700GiB Longhorn volume is committed and awaiting the wipe. More
nodes land within days, after which Longhorn replaces hostPath and the
preserved media volume (ADR-0011, Proposed).

**Toward goal:** "Blank metal → cluster member with one human act" in
`desired-state/goals.md` — w1 exercised that path end to end for the
first time on hardware that was *not* the control plane, and the one
human act held: a wallet signature to unseal, a wallet signature to
approve, nothing else. The storage work serves it too: replicated
volumes are what make a node wipe cheap enough for provisioning to stay
routine.

**Out of scope:**
- Replicating the media library for its own sake — ADR-0011 demotes
  media to an ordinary volume; the durability that matters is app state
  (sonarr/radarr/jellyfin metadata), which no design has protected yet.
- Encrypting the Longhorn volume (thread `8e46f3a5`) — needs hub key
  derivation for user volumes, not just `systemDiskEncryption`.
- HTTPS over the mesh (task `75c8b6b3`) — unchanged deferral.
- A second control plane / etcd HA — the new nodes are workers until
  there are three, and auto-bootstrap actively refuses multi-CP.
