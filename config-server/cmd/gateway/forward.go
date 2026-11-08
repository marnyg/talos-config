// Untagged on purpose: a flag type, no transport, so it compiles and
// tests without -tags iroh (main.go is tagged). Same shape as irohup's
// forwards.
package main

import (
	"fmt"
	"net"
	"sort"
	"strings"
)

// forwards is the repeatable -forward flag: facet → Service host:port,
// one accept-table row each (ADR-0048). Any facet name — the gateway
// serves what it is told to; the recipe decides who may reach it and
// a facet the recipe never names is reached by nobody.
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
