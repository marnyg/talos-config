package main

// Mesh tunnel DNS: the hub answers A queries for <name>.<zone> on its
// overlay address (udp/53), served over the nebula netstack rather than
// a kernel socket — see nebstack, and lighthouse.serve_dns being off in
// nebconf.go.
//
// The zone is a pure function of (git, master): machine labels come from
// meta.yaml, device labels from the enrolled-device list, and every
// address is derived. That makes it strictly stronger than nebula's
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

	"golang.org/x/net/dns/dnsmessage"

	"github.com/marnyg/talos-config/config-server/machines"
	"github.com/marnyg/talos-config/config-server/nebderive"
	"github.com/marnyg/talos-config/config-server/nebstack"
)

const dnsTTL = 300 // seconds; records only change on redeploy+unseal

var dnsLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// machineDNSName returns the machine's mesh DNS label: the meta.yaml
// name if set, else the MAC with dashes.
func machineDNSName(mac string, m machines.Machine) string {
	if m.Name != "" {
		return strings.ToLower(strings.TrimSpace(m.Name))
	}
	return strings.ReplaceAll(mac, ":", "-")
}

// meshDNSZone is the on-mesh DNS zone; the constant (and the rationale
// for `.internal`) lives in nebderive.DNSZone so nebup shares it.
const meshDNSZone = nebderive.DNSZone

// machineMeshIP returns the machine's overlay address: the explicit
// meta.yaml meshIP override if set, else derived from the MAC.
//
// The override exists because certificates bake the address. A derived
// collision on wg0 is a config edit away from fixed; on the mesh it
// also invalidates a minted cert, so the escape hatch has to be there
// before the first collision, not after.
func machineMeshIP(master []byte, mac string, m machines.Machine, subnet netip.Prefix) (netip.Addr, error) {
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

// buildMeshZone computes label → overlay address for every machine, every
// named device, and the hub.
//
// It is also where derived-address collisions are caught. nebderive
// deliberately leaves that to the caller, and this is the one place that
// sees every member at once, so an unseal fails loudly here rather than
// minting two certs that claim the same address.
func buildMeshZone(master []byte, subnet netip.Prefix, byMAC map[string]machines.Machine, devices []nebDevice) (map[string]netip.Addr, error) {
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
			return fmt.Errorf("mesh address collision: %s and %s both get %s (set meshIP in meta.yaml, or rename the device, to resolve)", prev, who, ip)
		}
		labelOwner[label] = who
		addrOwner[ip] = who
		zone[label] = ip
		return nil
	}

	for _, mac := range slices.Sorted(maps.Keys(byMAC)) {
		m := byMAC[mac]
		ip, err := machineMeshIP(master, mac, m, subnet)
		if err != nil {
			return nil, err
		}
		if err := claim(machineDNSName(mac, m), "machine "+mac, ip); err != nil {
			return nil, err
		}
	}
	for _, d := range devices {
		ip, err := nebderive.DeviceIP(master, d.name, subnet)
		if err != nil {
			return nil, err
		}
		if err := claim(d.name, "device "+d.name, ip); err != nil {
			return nil, err
		}
	}
	return zone, nil
}

// dnsRespond answers one raw DNS query against the zone. Pure function
// (tested without the netstack). Returns nil for unanswerable input.
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
		ip, ok := zone[strings.TrimSuffix(qname, "."+domain)]
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
// against the (immutable) zone. The listener binds the hub's own overlay
// address, which is what peers are told to use as their resolver.
func serveMeshDNS(svc *nebstack.Service, zone map[string]netip.Addr, domain string) error {
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
			if resp := dnsRespond(zone, domain, buf[:n]); resp != nil {
				_, _ = conn.WriteTo(resp, from)
			}
		}
	}()
	log.Printf("mesh dns: %d name(s) under .%s on %s:53", len(zone), domain, svc.OverlayAddr())
	return nil
}
