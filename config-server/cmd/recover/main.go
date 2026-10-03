// Command recover derives break-glass values offline from the fleet
// master — the values an operator needs when the hub is down or being
// re-rooted. It never dials anything: every output is a pure function
// of (master, identifier), which is the whole point — recovery works
// from a laptop and a wallet, nothing else (invariant 3).
//
//	recover -sig <hex> -recovery -uuid <uuid> # disk recovery passphrase (LUKS slot 1)
//	recover -sig <hex> -recovery -mac <mac>   # same, for an install before 2026-10-03 (meta.yaml installMAC)
//	recover -sig <hex> -age-recipient         # age recipient, for talos/age-recipient.txt
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
)

func main() {
	var (
		sig       = flag.String("sig", "", "unseal signature (hex over the master message)")
		master    = flag.String("master", "", "master key (hex); alternative to -sig")
		uuid      = flag.String("uuid", "", "machine SMBIOS UUID (with -recovery; meta.yaml uuid)")
		mac       = flag.String("mac", "", "machine install MAC (with -recovery, installs before 2026-10-03; meta.yaml installMAC)")
		recovery  = flag.Bool("recovery", false, "print the machine's disk recovery passphrase (needs -uuid, or -mac for a grandfathered install)")
		ageRecip  = flag.Bool("age-recipient", false, "print the wallet-derived age recipient; commit it as talos/age-recipient.txt")
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
		switch {
		case *uuid != "" && *mac != "":
			log.Fatal("-recovery takes -uuid or -mac, not both (meta.yaml: installMAC set → -mac, else -uuid)")
		case *uuid != "":
			fmt.Println(masterderive.RecoveryPassphrase(m, *uuid))
		case *mac != "":
			normMAC := strings.ToLower(strings.ReplaceAll(*mac, "-", ":"))
			fmt.Println(masterderive.RecoveryPassphraseMAC(m, normMAC))
		default:
			log.Fatal("-recovery needs -uuid (or -mac for an install before 2026-10-03)")
		}
	case *ageRecip:
		_, recipient := masterderive.AgeIdentity(m)
		fmt.Println(recipient)
	case *masterHex:
		fmt.Println(*master)
	default:
		flag.Usage()
		log.Fatal("pick one of -recovery, -age-recipient, -master-hex")
	}
}
