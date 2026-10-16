package main

import (
	"fmt"
	"net"
	"slices"
	"sort"
	"strings"

	"github.com/marnyg/talos-config/config-server/policy"
)

// bridge is one local listener: TCP on Listen → facet on the member
// called Name.
type bridge struct {
	Name, Facet, Listen string
}

type bridges []bridge

func (b *bridges) String() string {
	var parts []string
	for _, x := range *b {
		parts = append(parts, x.Name+"/"+x.Facet+"="+x.Listen)
	}
	return strings.Join(parts, ",")
}

// Set parses <member>/<facet>=<host:port>.
func (b *bridges) Set(s string) error {
	target, listen, ok := strings.Cut(s, "=")
	name, facet, ok2 := strings.Cut(target, "/")
	if !ok || !ok2 || name == "" || facet == "" {
		return fmt.Errorf("want <member>/<facet>=<host:port>, got %q", s)
	}
	if _, _, err := net.SplitHostPort(listen); err != nil {
		return fmt.Errorf("%q: %w", s, err)
	}
	*b = append(*b, bridge{Name: strings.ToLower(strings.TrimSpace(name)), Facet: facet, Listen: listen})
	return nil
}

// forward is one served facet: a stream admitted under Facet is spliced
// to the loopback Target (the accept table's other half, nodeagent
// Options.Forward). Device facets only (policy.KindDevice): what a
// device may serve — its own sshd (ADR-0038).
type forwards map[string]string

func (f *forwards) String() string {
	var parts []string
	for facet, target := range *f {
		parts = append(parts, facet+"="+target)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// Set parses <facet>=<host:port>.
func (f *forwards) Set(s string) error {
	facet, target, ok := strings.Cut(s, "=")
	if !ok || facet == "" {
		return fmt.Errorf("want <facet>=<host:port>, got %q", s)
	}
	if !slices.Contains(policy.Facets(policy.KindDevice), facet) {
		return fmt.Errorf("%q is not a device facet (%v)", facet, policy.Facets(policy.KindDevice))
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		return fmt.Errorf("%q: %w", s, err)
	}
	if *f == nil {
		*f = forwards{}
	}
	if _, dup := (*f)[facet]; dup {
		return fmt.Errorf("-forward %s given twice", facet)
	}
	(*f)[facet] = target
	return nil
}
