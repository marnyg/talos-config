package boot

import (
	"flag"
	"testing"
)

func TestIDFlags(t *testing.T) {
	const good = "ed:79547a961cf58b525d4a65c32dca8db46c5aa568526f56911433f7410dcd769b"
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	var one ID
	var many IDs
	fs.Var(&one, "one", "")
	fs.Var(&many, "many", "")
	if err := fs.Parse([]string{"-one", " " + good + " ", "-many", good, "-many", good}); err != nil {
		t.Fatal(err)
	}
	if one.ActorID() != good || len(many) != 2 || many[1] != good {
		t.Fatalf("got one=%q many=%v", one, many)
	}
	for _, bad := range []string{"", "ed:zz", "0xabc", "nope"} {
		var id ID
		if err := id.Set(bad); err == nil {
			t.Errorf("Set(%q) should fail", bad)
		}
		var l IDs
		if err := l.Set(bad); err == nil || len(l) != 0 {
			t.Errorf("IDs.Set(%q) should fail and append nothing", bad)
		}
	}
	var unset ID
	if unset.ActorID() != "" {
		t.Fatal("zero ID should mean not given")
	}
}
