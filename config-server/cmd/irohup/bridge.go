package main

import (
	"fmt"
	"net"
	"strings"

	"github.com/marnyg/talos-config/config-server/nebderive"
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
	*b = append(*b, bridge{Name: nebderive.Normalize(name), Facet: facet, Listen: listen})
	return nil
}
