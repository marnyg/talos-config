package boot

import (
	"fmt"
	"strings"

	"github.com/marnyg/talos-config/protocol/cert"
)

// ID is a flag.Value holding one actor id, validated on Set; the zero
// value means "not given".
type ID cert.ActorID

func (i *ID) String() string { return string(*i) }

// Set validates s as an actor id.
func (i *ID) Set(s string) error {
	id := cert.ActorID(strings.TrimSpace(s))
	if err := id.Validate(); err != nil {
		return err
	}
	*i = ID(id)
	return nil
}

// ActorID is the id, "" when not given.
func (i ID) ActorID() cert.ActorID { return cert.ActorID(i) }

// IDs is a repeatable flag.Value of actor ids (-customer, -member),
// each validated on Set.
type IDs []cert.ActorID

func (l *IDs) String() string { return fmt.Sprint([]cert.ActorID(*l)) }

// Set validates and appends s.
func (l *IDs) Set(s string) error {
	var id ID
	if err := id.Set(s); err != nil {
		return err
	}
	*l = append(*l, id.ActorID())
	return nil
}
