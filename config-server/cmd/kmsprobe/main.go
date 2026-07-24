// Command kmsprobe validates the KMS wire path end-to-end without
// touching any disks: it Seals a random probe value for a UUID and
// Unseals it again against a live endpoint — the exact dial path a
// Talos node uses at boot (gRPC, TLS for https:// endpoints).
//
//	kmsprobe -endpoint https://host:443 -uuid 00000000-dead-beef-0000-000000000000
//
// Expected outcomes:
//   - sealed server:  Unavailable
//   - unsealed:       roundtrip OK (undeclared UUIDs ride the
//     session-seal grace; they show as a warning on /status until the
//     server restarts)
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	kmsapi "github.com/siderolabs/kms-client/api/kms"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	var (
		endpoint = flag.String("endpoint", "", "KMS endpoint (https://host:port for TLS, grpc://host:port for plaintext)")
		uuid     = flag.String("uuid", "00000000-dead-beef-0000-000000000000", "node UUID to probe with")
		sni      = flag.String("sni", "", "TLS server name override (when dialing an IP directly)")
	)
	flag.Parse()
	if *endpoint == "" {
		flag.Usage()
		log.Fatal("missing -endpoint")
	}

	var creds credentials.TransportCredentials
	target := *endpoint
	switch {
	case strings.HasPrefix(target, "https://"):
		target = strings.TrimPrefix(target, "https://")
		creds = credentials.NewTLS(&tls.Config{ServerName: *sni})
	case strings.HasPrefix(target, "grpc://"):
		target = strings.TrimPrefix(target, "grpc://")
		creds = insecure.NewCredentials()
	default:
		creds = insecure.NewCredentials()
	}

	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(creds))
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer conn.Close() //nolint:errcheck
	client := kmsapi.NewKMSServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	probe := make([]byte, 32)
	if _, err := rand.Read(probe); err != nil {
		log.Fatal(err)
	}

	sealed, err := client.Seal(ctx, &kmsapi.Request{NodeUuid: *uuid, Data: probe})
	if err != nil {
		log.Fatalf("Seal: %v", err)
	}
	fmt.Printf("sealed: %d bytes\n", len(sealed.Data))

	out, err := client.Unseal(ctx, &kmsapi.Request{NodeUuid: *uuid, Data: sealed.Data})
	if err != nil {
		log.Fatalf("Unseal: %v", err)
	}
	if !bytes.Equal(out.Data, probe) {
		log.Fatal("roundtrip MISMATCH")
	}
	fmt.Println("roundtrip OK")
}
