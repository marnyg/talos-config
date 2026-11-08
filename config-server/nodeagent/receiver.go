package nodeagent

import (
	"fmt"
	"net"
	"slices"
	"strconv"

	"github.com/marnyg/talos-config/config-server/policy"
)

// receiverTable derives what this member accepts and advertises from
// Options.Kind/Forward/Serve. The accept table is exactly Forward ∪ Serve (ADR-0048):
// the receiver is the authority on what it serves, the recipe on who may
// reach it. No membership check against a compiled vocabulary — that is
// what made a new facet a receiver rebuild. Returns the sorted facet
// list, the port each is presented on, the ALPN → facet accept table,
// the ALPNs to bind, and the reach-me-at's cav.facet entries.
func receiverTable(kind policy.Kind, forward map[string]string, serve map[string]StreamHandler) (*receiver, error) {
	if kind == "" {
		kind = policy.KindNode
	}
	if !slices.Contains(policy.Kinds, kind) {
		return nil, fmt.Errorf("nodeagent: unknown receiver kind %q", kind)
	}
	facets := make([]string, 0, len(forward)+len(serve))
	// ports is the port each facet is presented on (ADR-0047): its
	// natural port when the compiled Default has one, else — for a
	// forward — the port its target listens on, the one fact the receiver
	// has that no table does.
	ports := make(map[string]uint16, len(forward)+len(serve))
	for f, target := range forward {
		if f == "" {
			return nil, fmt.Errorf("nodeagent: forward with an empty facet name")
		}
		_, tp, err := net.SplitHostPort(target)
		if err != nil {
			return nil, fmt.Errorf("nodeagent: forward %s=%q: %w", f, target, err)
		}
		if _, dup := serve[f]; dup {
			return nil, fmt.Errorf("nodeagent: %q is both forwarded and served", f)
		}
		port := policy.FacetPort(f)
		if port == 0 {
			n, err := strconv.ParseUint(tp, 10, 16)
			if err != nil || n == 0 {
				return nil, fmt.Errorf("nodeagent: forward %s=%q: no natural port and target port %q is not a port", f, target, tp)
			}
			port = uint16(n)
		}
		ports[f] = port
		facets = append(facets, f)
	}
	for f, h := range serve {
		if f == "" {
			return nil, fmt.Errorf("nodeagent: serve with an empty facet name")
		}
		if h == nil {
			return nil, fmt.Errorf("nodeagent: serve %s: nil handler", f)
		}
		port := policy.FacetPort(f)
		if port == 0 {
			return nil, fmt.Errorf("nodeagent: serve %s: facet has no natural port to advertise", f)
		}
		ports[f] = port
		facets = append(facets, f)
	}
	slices.Sort(facets)
	// The reach-me-at advertises each facet twice (ADR-0047): bare, which
	// is what reads as this member's kind (Zone) and what pre-0047 daemons
	// understand, and `<facet>@<port>`, which is what a presentation reads
	// a port against. Additive on purpose: drop the bare form only once
	// no daemon reads it.
	adverts := make([]string, 0, 2*len(facets))
	for _, f := range facets {
		adverts = append(adverts, f, Advertise(f, ports[f]))
	}
	accept := map[string]string{}
	alpns := make([]string, 0, len(facets))
	for _, f := range facets {
		accept[policy.ALPN(f)] = f
		alpns = append(alpns, policy.ALPN(f))
	}

	return &receiver{facets: facets, ports: ports, accept: accept, alpns: alpns, adverts: adverts}, nil
}

// receiver is receiverTable's result.
type receiver struct {
	facets  []string
	ports   map[string]uint16
	accept  map[string]string
	alpns   []string
	adverts []string
}
