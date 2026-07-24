// Command wgping is the client half of the WireGuard-over-fly spike:
// a userspace WG client (no root) that connects to the config server's
// tunnel endpoint and fetches the TCP hello through it.
//
//	wgping -genkey                 # print a keypair
//	wgping -endpoint <ip:port> -server-pub <hex> -key <hex>
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"os"

	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

func genkey() {
	priv := make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		log.Fatal(err)
	}
	// curve25519 clamping
	priv[0] &= 248
	priv[31] = (priv[31] & 127) | 64
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("private: %s\npublic:  %s\n", hex.EncodeToString(priv), hex.EncodeToString(pub))
}

func main() {
	var (
		doGenkey  = flag.Bool("genkey", false, "generate a keypair and exit")
		endpoint  = flag.String("endpoint", "", "server endpoint ip:port")
		serverPub = flag.String("server-pub", "", "server public key (hex)")
		key       = flag.String("key", "", "client private key (hex)")
		addr      = flag.String("addr", "10.99.0.2", "client tunnel address")
		target    = flag.String("target", "10.99.0.1:80", "TCP target through the tunnel")
	)
	flag.Parse()

	if *doGenkey {
		genkey()
		return
	}
	if *endpoint == "" || *serverPub == "" || *key == "" {
		flag.Usage()
		os.Exit(2)
	}

	// The WG endpoint parser wants a literal IP.
	udpAddr, err := net.ResolveUDPAddr("udp", *endpoint)
	if err != nil {
		log.Fatalf("resolving endpoint: %v", err)
	}

	tun, tnet, err := netstack.CreateNetTUN(
		[]netip.Addr{netip.MustParseAddr(*addr)}, []netip.Addr{}, 1280)
	if err != nil {
		log.Fatal(err)
	}
	dev := device.NewDevice(tun, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "wgping: "))

	uapi := fmt.Sprintf(
		"private_key=%s\npublic_key=%s\nendpoint=%s\nallowed_ip=0.0.0.0/0\npersistent_keepalive_interval=5\n",
		*key, *serverPub, udpAddr.String())
	if err := dev.IpcSet(uapi); err != nil {
		log.Fatal(err)
	}
	if err := dev.Up(); err != nil {
		log.Fatal(err)
	}

	c, err := tnet.Dial("tcp", *target)
	if err != nil {
		log.Fatalf("dial through tunnel: %v", err)
	}
	defer c.Close()
	body, err := io.ReadAll(c)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(body))
}
