package main

// Tunnel DNS: the hub answers A queries for <name>.<domain> on its
// tunnel address (udp/53) — MagicDNS without the magic. The zone is
// derived entirely from the repo at unseal time (machine names from
// meta.yaml, admin peers, "hub" for the server itself); there is no
// zone file and no state. Out-of-zone queries are REFUSED so split-DNS
// stub resolvers fail over to their normal upstream.

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

	"github.com/marnyg/talos-config/config-server/wgderive"
)

const dnsTTL = 300 // seconds; records only change on redeploy+unseal

var dnsLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// machineDNSName returns the machine's tunnel DNS label: the meta.yaml
// name if set, else the MAC with dashes.
func machineDNSName(mac string, m machine) string {
	if m.Name != "" {
		return strings.ToLower(strings.TrimSpace(m.Name))
	}
	return strings.ReplaceAll(mac, ":", "-")
}

// buildDNSZone computes label → tunnel address for every peer plus the
// hub, failing on invalid labels and collisions (rename in meta.yaml
// or the admin peer list to resolve).
func buildDNSZone(machines map[string]machine, w *wgSettings) (map[string]netip.Addr, error) {
	zone := map[string]netip.Addr{"hub": w.serverIP}
	owner := map[string]string{"hub": "the server"}
	claim := func(label, who string, ip netip.Addr) error {
		if !dnsLabelRe.MatchString(label) {
			return fmt.Errorf("%s: invalid DNS label %q", who, label)
		}
		if prev, dup := owner[label]; dup {
			return fmt.Errorf("DNS name collision: %s and %s both want %q", prev, who, label)
		}
		owner[label] = who
		zone[label] = ip
		return nil
	}
	for _, mac := range slices.Sorted(maps.Keys(machines)) {
		ip, err := w.machineTunnelIP(mac, machines[mac])
		if err != nil {
			return nil, err
		}
		if err := claim(machineDNSName(mac, machines[mac]), "machine "+mac, ip); err != nil {
			return nil, err
		}
	}
	for _, name := range w.admins {
		name = wgderive.NormalizeAdmin(name)
		if name == "" {
			continue
		}
		ip, err := wgderive.AdminTunnelIP(w.master, name, w.subnet)
		if err != nil {
			return nil, err
		}
		if err := claim(name, "admin "+name, ip); err != nil {
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

// serveDNS starts the UDP listener on the tunnel address and answers
// queries against the (immutable) zone.
func (w *wgSettings) serveDNS(zone map[string]netip.Addr) error {
	conn, err := w.tnet.ListenUDP(&net.UDPAddr{IP: w.serverIP.AsSlice(), Port: 53})
	if err != nil {
		return fmt.Errorf("tunnel dns listener: %w", err)
	}
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				log.Printf("wg dns read: %v", err)
				return
			}
			if resp := dnsRespond(zone, w.dnsDomain, buf[:n]); resp != nil {
				_, _ = conn.WriteTo(resp, from)
			}
		}
	}()
	return nil
}
