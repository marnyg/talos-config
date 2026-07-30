// Command recover derives break-glass values offline from the fleet
// master — the values an operator needs when the hub is down or being
// re-rooted. It never dials anything: every output is a pure function
// of (master, identifier), which is the whole point — recovery works
// from a laptop and a wallet, nothing else (invariant 3).
//
//	recover -sig <hex> -recovery -mac <mac>   # disk recovery passphrase (LUKS slot 1)
//	recover -sig <hex> -age-recipient         # age recipient, for talos/age-recipient.txt
//	recover -sig <hex> -ca-fingerprint        # mesh CA fingerprint, for MESH_CA_PIN
//	recover -sig <hex> -master-hex            # raw master, for WG_MASTER_KEY (dev)
//
// -master <hex> is accepted anywhere -sig is, for dev masters that
// never came from a signature. The signature is the one produced by
// `cast wallet sign` over masterderive.MasterMessage — the same
// signature that unseals the hub, handled with the same care.
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/marnyg/talos-config/config-server/masterderive"
	"github.com/marnyg/talos-config/config-server/nebderive"
)

func main() {
	var (
		sig       = flag.String("sig", "", "unseal signature (hex over the master message)")
		master    = flag.String("master", "", "master key (hex); alternative to -sig")
		mac       = flag.String("mac", "", "machine MAC (with -recovery)")
		recovery  = flag.Bool("recovery", false, "print the machine's disk recovery passphrase (needs -mac)")
		ageRecip  = flag.Bool("age-recipient", false, "print the wallet-derived age recipient; commit it as talos/age-recipient.txt")
		caFP      = flag.Bool("ca-fingerprint", false, "print the mesh CA fingerprint; pin it via MESH_CA_PIN")
		masterHex = flag.Bool("master-hex", false, "print the raw master key (handle like the signature itself)")
	)
	flag.Parse()

	if *sig != "" {
		m, err := masterderive.MasterFromSignatureHex(*sig)
		if err != nil {
			log.Fatalf("-sig: %v", err)
		}
		*master = hex.EncodeToString(m)
	}
	if *master == "" {
		log.Fatal("need -sig or -master")
	}
	m, err := masterderive.MasterFromHex(*master)
	if err != nil {
		log.Fatalf("-master: %v", err)
	}

	switch {
	case *recovery:
		if *mac == "" {
			log.Fatal("-recovery needs -mac")
		}
		normMAC := strings.ToLower(strings.ReplaceAll(*mac, "-", ":"))
		fmt.Println(masterderive.RecoveryPassphrase(m, normMAC))
	case *ageRecip:
		_, recipient := masterderive.AgeIdentity(m)
		fmt.Println(recipient)
	case *caFP:
		fp, err := nebderive.CAFingerprint(m)
		if err != nil {
			log.Fatalf("deriving CA fingerprint: %v", err)
		}
		fmt.Println(fp)
	case *masterHex:
		fmt.Println(*master)
	default:
		flag.Usage()
		log.Fatal("pick one of -recovery, -age-recipient, -ca-fingerprint, -master-hex")
	}
}
