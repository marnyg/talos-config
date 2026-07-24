// Command wgping is a userspace WG test client (no root) that connects
// to the config server's tunnel endpoint and fetches the TCP hello
// through it.
//
//	wgping -genkey                 # print a keypair
//	wgping -endpoint <ip:port> -server-pub <hex> -key <hex>
//	wgping -endpoint <ip:port> -master <hex> -mac <mac>   # impersonate a machine
//	wgping -sig <hex>              # unseal signature → print server pubkey (for pinning)
//	wgping -endpoint <ip:port> -sig <hex> -mac <mac>      # impersonate via signature
//	wgping -sig <hex> -recovery -mac <mac> # derive the disk recovery passphrase
//
// The -master/-sig modes derive the client key, tunnel address, and
// server public key exactly like the server does for a provisioned
// machine — an end-to-end check of the derivation contract.
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
	"strings"

	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/marnyg/talos-config/config-server/wgderive"
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
		master    = flag.String("master", "", "WG master key (hex); with -mac, derives -key/-addr/-server-pub")
		sig       = flag.String("sig", "", "unseal signature (hex over the master message); alternative to -master")
		mac       = flag.String("mac", "", "machine MAC to impersonate (with -master/-sig)")
		subnet    = flag.String("subnet", "10.99.0.0/24", "tunnel subnet (with -master/-sig)")
		recovery  = flag.Bool("recovery", false, "print the machine's disk recovery passphrase (with -master/-sig and -mac) and exit")
	)
	flag.Parse()

	if *doGenkey {
		genkey()
		return
	}
	if *sig != "" {
		m, err := wgderive.MasterFromSignatureHex(*sig)
		if err != nil {
			log.Fatalf("-sig: %v", err)
		}
		*master = hex.EncodeToString(m)
	}
	if *recovery {
		if *master == "" || *mac == "" {
			log.Fatal("-recovery needs -master/-sig and -mac")
		}
		m, err := wgderive.MasterFromHex(*master)
		if err != nil {
			log.Fatalf("-master: %v", err)
		}
		normMAC := strings.ToLower(strings.ReplaceAll(*mac, "-", ":"))
		fmt.Println(wgderive.RecoveryPassphrase(m, normMAC))
		return
	}
	if *master != "" {
		m, err := wgderive.MasterFromHex(*master)
		if err != nil {
			log.Fatalf("-master: %v", err)
		}
		serverPubKey := wgderive.PublicKey(wgderive.ServerKey(m))
		*serverPub = wgderive.KeyHex(serverPubKey)
		fmt.Printf("server pubkey: %s (hex %s)\n", wgderive.KeyBase64(serverPubKey), *serverPub)
		if *mac == "" {
			// No machine to impersonate: derivation info only (e.g. to
			// compute the --wg-server-pubkey pin).
			return
		}
		normMAC := strings.ToLower(strings.ReplaceAll(*mac, "-", ":"))
		priv := wgderive.MachineKey(m, normMAC)
		ip, err := wgderive.TunnelIP(m, normMAC, netip.MustParsePrefix(*subnet))
		if err != nil {
			log.Fatalf("deriving tunnel ip: %v", err)
		}
		*key = wgderive.KeyHex(priv)
		*addr = ip.String()
		log.Printf("derived: addr %s, pubkey %s", ip, wgderive.KeyBase64(wgderive.PublicKey(priv)))
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
