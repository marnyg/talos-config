package main

import (
	"context"
	"crypto/tls"
	stdx509 "crypto/x509"
	"io"
	"net"
	"testing"
	"time"

	"github.com/siderolabs/crypto/x509"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/marnyg/talos-config/config-server/machines"
	"github.com/marnyg/talos-config/protocol/cert"
)

// pipeFacet is a facetClient whose every Open is one net.Pipe; the
// server side accepts the other end. No name map, no iroh, no DNS.
type pipeFacet struct {
	accept chan net.Conn
}

func (p *pipeFacet) Peer() cert.ActorID { return cert.ActorID("ed:test") }
func (p *pipeFacet) Close() error       { return nil }
func (p *pipeFacet) Open(ctx context.Context) (io.ReadWriteCloser, error) {
	c, s := net.Pipe()
	select {
	case p.accept <- s:
		return c, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// pipeListener hands the server the conns pipeFacet opens.
type pipeListener struct {
	accept chan net.Conn
	done   chan struct{}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.accept:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *pipeListener) Close() error   { close(l.done); return nil }
func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

type fakeMachine struct {
	machineapi.UnimplementedMachineServiceServer
}

func (fakeMachine) ServiceList(context.Context, *emptypb.Empty) (*machineapi.ServiceListResponse, error) {
	return &machineapi.ServiceListResponse{Messages: []*machineapi.ServiceList{{
		Services: []*machineapi.ServiceInfo{svc("etcd", "Waiting")},
	}}}, nil
}

// TestClientOverFacetNoDNS: the machinery client's single endpoint is a
// name that resolves nowhere ("cp1.test.invalid") — it must reach the
// server through the facet dialer anyway, and TLS must verify that
// name against the server's SAN. Before facetResolver, gRPC's dns
// resolver produced zero addresses and the dialer never ran (seen
// live 2026-09-20: auto-bootstrap "unreachable" on a node that was
// admitting the hub's stream fine).
func TestClientOverFacetNoDNS(t *testing.T) {
	now := time.Now()
	ca, err := secrets.NewTalosCA(now)
	if err != nil {
		t.Fatal(err)
	}
	const san = "cp1.test.invalid"
	serverKP, err := x509.NewKeyPair(ca,
		x509.DNSNames([]string{san}),
		x509.NotBefore(now), x509.NotAfter(now.Add(time.Hour)),
		x509.KeyUsage(stdx509.KeyUsageDigitalSignature),
		x509.ExtKeyUsage([]stdx509.ExtKeyUsage{stdx509.ExtKeyUsageServerAuth}),
	)
	if err != nil {
		t.Fatal(err)
	}
	pool := stdx509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CrtPEM)
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{*serverKP.Certificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS13,
	})))
	machineapi.RegisterMachineServiceServer(srv, fakeMachine{})
	accept := make(chan net.Conn)
	ln := &pipeListener{accept: accept, done: make(chan struct{})}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(srv.Stop)

	b := newBootstrapper(t.TempDir(), testHubManager(t, nil, ""))
	m := machines.Machine{Dir: "/fake/cp1"}
	b.caCache[m.Dir] = &x509.PEMEncodedCertificateAndKey{Crt: ca.CrtPEM, Key: ca.KeyPEM}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	c, err := b.clientOver(ctx, m, san, &pipeFacet{accept: accept})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close() //nolint:errcheck
	resp, err := c.ServiceList(ctx)
	if err != nil {
		t.Fatalf("ServiceList over facet: %v", err)
	}
	var services []*machineapi.ServiceInfo
	for _, msg := range resp.GetMessages() {
		services = append(services, msg.GetServices()...)
	}
	if got := observeEtcd(services); got != etcdWaiting {
		t.Fatalf("observation = %v, want etcdWaiting", got)
	}
}
