# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Making a node wipe actually cheap. The storage layer exists
(Longhorn on 1073GB across both nodes, ADR-0011 implemented), but
nothing on the cluster is replicated yet: app config still lives on
`emptyDir`, so the 2-replica class has no users and sonarr/radarr/
jellyfin metadata dies on every pod restart. Moving that state onto
Longhorn is what converts the storage layer from installed to useful.

**Toward goal:** "Blank metal → cluster member with one human act" in
`desired-state/goals.md`. Replicated volumes are what make wiping a node
routine rather than an event — the same property that makes provisioning
cheap. Invariant 2's corollary ("no single node's disk is exempt from a
wipe") is the checkable form, and it is currently deviated from by
design while capacity is short.

**Out of scope:**
- Raising `longhorn-bulk` to 2 replicas — blocked on the new nodes
  (`da61bd8e`); the deviation is tracked, not forgotten.
- The etcd/DHCP invariant 7 fix (`6c456522`) — real, but its safe form
  needs a listen-subnet experiment with a rollback window, not a
  bolt-on to storage work.
- Encrypting the Longhorn volumes (`8e46f3a5`) — still needs hub key
  derivation for user volumes, and the posture argument weakened now
  that app state is headed onto those disks.
- HTTPS over the mesh (`75c8b6b3`) — unchanged deferral.
