package mesh

// Mesh tunnel DNS: the hub answers A queries for <name>.<zone> on its
// overlay address (udp/53), served over the nebula netstack rather than
// a kernel socket — see nebstack, and lighthouse.serve_dns being off in
// nebconf.go.
//
// The zone is a pure function of (git, master): machine labels come from
// meta.yaml, device labels from the enrolled-device list, and every
// address is derived. Service names are not declared at all: any
// <service>.<member> name resolves to the member's address (see
// dnsRespond), so exposing a service is purely an Ingress in git —
// vhosts behind the member's ingress, routed by Host header. That makes it strictly stronger than nebula's
// lighthouse DNS, which can only answer for hosts that have reported in
// — an unreachable machine still resolves here.
//
import (
	"fmt"
	"log"
	"maps"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strings"

	"github.com/slackhq/nebula"
	"golang.org/x/net/dns/dnsmessage"

	"github.com/marnyg/talos-config/config-server/machines"
	"github.com/marnyg/talos-config/config-server/nebderive"
	"github.com/marnyg/talos-config/config-server/nebstack"
)

const dnsTTL = 300 // seconds; records only change on redeploy+unseal

var dnsLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// MachineDNSName returns the machine's mesh DNS label: the meta.yaml
// name if set, else the MAC with dashes.
func MachineDNSName(mac string, m machines.Machine) string {
	if m.Name != "" {
		return strings.ToLower(strings.TrimSpace(m.Name))
	}
	return strings.ReplaceAll(mac, ":", "-")
}

// MachineMeshIP returns the machine's overlay address: the explicit
// meta.yaml meshIP override if set, else derived from the MAC.
//
// The override exists because certificates bake the address. A derived
// collision on wg0 is a config edit away from fixed; on the mesh it
// also invalidates a minted cert, so the escape hatch has to be there
// before the first collision, not after.
func MachineMeshIP(master []byte, mac string, m machines.Machine, subnet netip.Prefix) (netip.Addr, error) {
	if m.MeshIP != "" {
		ip, err := netip.ParseAddr(m.MeshIP)
		if err != nil {
			return netip.Addr{}, fmt.Errorf("machine %s: invalid meshIP %q: %w", mac, m.MeshIP, err)
		}
		if !subnet.Contains(ip) {
			return netip.Addr{}, fmt.Errorf("machine %s: meshIP %s is outside the mesh subnet %s", mac, ip, subnet)
		}
		return ip, nil
	}
	return nebderive.MachineIP(master, mac, subnet)
}

// buildMeshZone computes label → overlay address for every machine and
// the hub. Devices are not in this zone under ADR-0012: the device set
// is not enumerable from git, and each device's DNS name resolves only
// while its tunnel is live (see dnsRespond).
//
// This is also where derived-address collisions are caught. nebderive
// deliberately leaves that to the caller, and this is the one place
// that sees every git-derived member at once, so an unseal fails loudly
// here rather than minting two certs that claim the same address.
func buildMeshZone(master []byte, subnet netip.Prefix, byMAC map[string]machines.Machine) (map[string]netip.Addr, error) {
	hubIP, err := nebderive.HubIP(subnet)
	if err != nil {
		return nil, err
	}

	zone := map[string]netip.Addr{nebderive.HubName: hubIP}
	labelOwner := map[string]string{nebderive.HubName: "the hub"}
	addrOwner := map[netip.Addr]string{hubIP: "the hub"}

	claim := func(label, who string, ip netip.Addr) error {
		if !dnsLabelRe.MatchString(label) {
			return fmt.Errorf("%s: invalid DNS label %q", who, label)
		}
		if prev, dup := labelOwner[label]; dup {
			return fmt.Errorf("mesh DNS name collision: %s and %s both want %q", prev, who, label)
		}
		if prev, dup := addrOwner[ip]; dup {
			return fmt.Errorf("mesh address collision: %s and %s both get %s (set meshIP in meta.yaml to resolve)", prev, who, ip)
		}
		labelOwner[label] = who
		addrOwner[ip] = who
		zone[label] = ip
		return nil
	}

	for _, mac := range slices.Sorted(maps.Keys(byMAC)) {
		m := byMAC[mac]
		ip, err := MachineMeshIP(master, mac, m, subnet)
		if err != nil {
			return nil, err
		}
		if err := claim(MachineDNSName(mac, m), "machine "+mac, ip); err != nil {
			return nil, err
		}
	}
	return zone, nil
}

// devicePeers is the peer-supplier the DNS resolver uses to answer for
// live device names. Parameter, not a method, so dnsRespond stays pure
// and testable without a running nebula.
type devicePeers func() []nebula.ControlHostInfo

// liveDeviceZone returns label → overlay address for every live device:
// any peer whose cert.Name is not already in the static (git) zone,
// whose group is one of the device groups, and whose derived address
// matches the cert. The derivation match is the same check the /config
// gate uses (ADR-0012): a peer's cert is only usable at the name+addr
// its enrollment signature bound.
func liveDeviceZone(master []byte, subnet netip.Prefix, static map[string]netip.Addr, peers []nebula.ControlHostInfo) map[string]netip.Addr {
	out := map[string]netip.Addr{}
	for _, hi := range peers {
		if len(hi.VpnAddrs) == 0 {
			continue
		}
		name := hi.Cert.Name()
		if _, isStatic := static[name]; isStatic {
			continue
		}
		if !dnsLabelRe.MatchString(name) {
			continue
		}
		var isDevice bool
		for _, g := range hi.Cert.Groups() {
			if g == GroupAdmins || g == GroupMedia {
				isDevice = true
				break
			}
		}
		if !isDevice {
			continue
		}
		want, err := nebderive.DeviceIP(master, name, subnet)
		if err != nil || want != hi.VpnAddrs[0] {
			continue
		}
		out[name] = hi.VpnAddrs[0]
	}
	return out
}

// dnsRespond answers one raw DNS query against the zone. Pure function
// (tested without the netstack). Returns nil for unanswerable input.
// The caller passes a single merged zone; precedence between static
// and live names is settled before the merge — liveDeviceZone never
// reports a name that exists in the static zone, and enrollment
// refuses colliding names in the first place.
func dnsRespond(zone map[string]netip.Addr, domain string, req []byte) []byte {
	var p dnsmessage.Parser
	hdr, err := p.Start(req)
	if err != nil || hdr.Response {
		return nil
	}
	q, err := p.Question()
	if err != nil {
		return nil
	}

	qname := strings.ToLower(strings.TrimSuffix(q.Name.String(), "."))
	rcode := dnsmessage.RCodeSuccess
	var answer netip.Addr
	switch {
	case qname == domain:
		// The apex exists but has no records.
	case !strings.HasSuffix(qname, "."+domain):
		rcode = dnsmessage.RCodeRefused
	default:
		rest := strings.TrimSuffix(qname, "."+domain)
		ip, ok := zone[rest]
		if !ok {
			// Scoped service names: <service>.<member> resolves to the
			// member. One extra label only, and an exact member match
			// wins above, so a service name can never shadow a member.
			if _, member, cut := strings.Cut(rest, "."); cut && !strings.Contains(member, ".") {
				ip, ok = zone[member]
			}
		}
		switch {
		case !ok:
			rcode = dnsmessage.RCodeNameError
		case q.Type == dnsmessage.TypeA && q.Class == dnsmessage.ClassINET:
			answer = ip
			// Known name, other type: NOERROR with an empty answer.
		}
	}

	b := dnsmessage.NewBuilder(make([]byte, 0, 512), dnsmessage.Header{
		ID:               hdr.ID,
		Response:         true,
		Authoritative:    true,
		RecursionDesired: hdr.RecursionDesired,
		RCode:            rcode,
	})
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil
	}
	if err := b.Question(q); err != nil {
		return nil
	}
	if answer.IsValid() {
		if err := b.StartAnswers(); err != nil {
			return nil
		}
		err := b.AResource(
			dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: dnsTTL},
			dnsmessage.AResource{A: answer.As4()},
		)
		if err != nil {
			return nil
		}
	}
	out, err := b.Finish()
	if err != nil {
		return nil
	}
	return out
}

// serveMeshDNS starts the overlay UDP/53 listener and answers queries
// against the static zone (hub + machines) layered with live devices.
// The listener binds the hub's own overlay address, which is what peers
// are told to use as their resolver.
func serveMeshDNS(svc *nebstack.Service, static map[string]netip.Addr, domain string, master []byte, subnet netip.Prefix, peers devicePeers) error {
	conn, err := svc.ListenUDP(&net.UDPAddr{Port: 53})
	if err != nil {
		return fmt.Errorf("mesh dns listener: %w", err)
	}
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				log.Printf("mesh dns read: %v", err)
				return
			}
			// Merge static + live for every request; the live set is
			// small and cheap, and copying keeps dnsRespond pure.
			merged := make(map[string]netip.Addr, len(static)+4)
			maps.Copy(merged, static)
			if peers != nil {
				for k, v := range liveDeviceZone(master, subnet, static, peers()) {
					merged[k] = v
				}
			}
			if resp := dnsRespond(merged, domain, buf[:n]); resp != nil {
				_, _ = conn.WriteTo(resp, from)
			}
		}
	}()
	log.Printf("mesh dns: %d static name(s) under .%s on %s:53 (devices resolve while live)", len(static), domain, svc.OverlayAddr())
	return nil
}
