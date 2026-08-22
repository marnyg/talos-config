# mesh-policy.ncl — snapshot invariants for talos/mesh-policy.yaml
# (talos-config-6z9).
#
# Sibling of verification/quint/: the quint models verify the DESIGN
# over all traces (small closed world); these contracts enforce the
# per-snapshot invariants on the REAL durable artifact — the actual
# policy file in git. Temporal properties (nonce single-use, crash-
# anywhere survival) stay quint-only; anything checkable on one value
# belongs here, where it runs against real data instead of a toy world.
#
# Mirrors two layers of enforcement:
#   1. parsePolicy (config-server/mesh/nebpolicy.go): closed schema,
#      proto/port ranges, exactly-one-of host/group, closed group set,
#      non-empty scopes.
#   2. Design stances only guarded by tests/comments today:
#      - node inbound never names `group: machines` (nebmachine_test:
#        machines must not reach each other's control surfaces).
#      - device inbound is ICMP-only (devices initiate; replies are
#        admitted statefully — see the file's own comment).
#
# Check: ./check.sh  (nickel export check.ncl)
let groups = ["admins", "media", "machines"] in
let protos = ["any", "tcp", "udp", "icmp"] in

let Proto = std.contract.from_predicate (fun p =>
  std.is_string p && std.array.elem p protos)
in

# "any" or "1"-"65535"; the YAML field is a string (numbers quoted).
let Port = std.contract.from_predicate (fun p =>
  std.is_string p
  && (p == "any"
    || (std.string.is_match "^[0-9]{1,5}$" p
      && (let n = std.string.to_number p in n >= 1 && n <= 65535))))
in

# The closed group set: a typoed group would silently admit nobody
# (firewall matches groups signed into certs), so it is rejected here
# exactly as parsePolicy rejects it at load.
let Group = std.contract.from_predicate (fun g =>
  std.is_string g && std.array.elem g groups)
in

# Closed record (no `..`): a typoed key like `hosts` fails loudly,
# mirroring yaml.Decoder.KnownFields(true).
let RuleShape = {
  port | Port,
  proto | Proto,
  host | String | optional,
  group | Group | optional,
}
in

# Exactly one of host/group per rule.
let ExactlyOneTarget = std.contract.from_predicate (fun r =>
  std.record.has_field "host" r != std.record.has_field "group" r)
in

let Rule = std.contract.Sequence [RuleShape, ExactlyOneTarget] in

# A member class with no inbound rules drops everything, even ICMP —
# refused, same stance as parsePolicy.
let NonEmptyInbound = std.contract.from_predicate (fun rules =>
  std.array.length rules > 0)
in

let Scope = {
  inbound | std.contract.Sequence [Array Rule, NonEmptyInbound],
}
in

# Design stance: machines must not reach each other over the mesh
# (compromised-node blast radius). The Go side guards this with a
# test; here it is structural — a `group: machines` rule under `node`
# fails the export.
let NodeIsolatesMachines = std.contract.from_predicate (fun scope =>
  scope.inbound
  |> std.array.all (fun r =>
    !(std.record.has_field "group" r) || r.group != "machines"))
in

# Design stance: a device only initiates; its inbound admits ICMP and
# nothing else (stateful firewall handles replies to flows it opened).
let DeviceInboundIcmpOnly = std.contract.from_predicate (fun scope =>
  scope.inbound |> std.array.all (fun r => r.proto == "icmp"))
in

{
  Policy = {
    hub | Scope,
    node | std.contract.Sequence [Scope, NodeIsolatesMachines],
    device | std.contract.Sequence [Scope, DeviceInboundIcmpOnly],
  },
}
