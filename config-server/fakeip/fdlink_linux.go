//go:build linux

package fakeip

import (
	"fmt"

	"gvisor.dev/gvisor/pkg/tcpip/link/fdbased"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

// NewFDLink is the link for a tun the caller already holds as a file
// descriptor — Android's VpnService.Builder.establish() (GOOS=android
// satisfies the linux constraint; bionic has the same tun semantics:
// one IP packet per read, no ethernet header). gvisor's fdbased picks
// a readv dispatcher for a non-socket fd. The fd is the stack's from
// here: closing the returned endpoint's stack does not close it — the
// owner does, after Stack.Close.
func NewFDLink(fd int, mtu uint32) (stack.LinkEndpoint, error) {
	link, err := fdbased.New(&fdbased.Options{FDs: []int{fd}, MTU: mtu, EthernetHeader: false})
	if err != nil {
		return nil, fmt.Errorf("fakeip: fdbased: %w", err)
	}
	return link, nil
}
