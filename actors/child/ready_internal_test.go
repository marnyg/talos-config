package child

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/envelope"
)

// A #task that lands before Run has handed the agent its chain waits
// for it — it is not refused in that instant (the window the first
// child on the gated relay fell into). A chain that never comes is
// still a refusal, after readyWait; and once set up, a missing chain
// is refused at once, as before.
func TestTaskWaitsForSetup(t *testing.T) {
	defer func(d time.Duration) { readyWait = d }(readyWait)
	readyWait = 300 * time.Millisecond
	inv := &actor.Invocation{Envelope: &envelope.Envelope{Payload: []byte(`{"prompt":"hi"}`)}}

	// Set up late, with no chain: the refusal waits for the setup and
	// is the outfit one, not "not set up".
	ag := newAgent(context.Background(), nil, "ed:p", TaskOptions{}, nil)
	go func() {
		time.Sleep(100 * time.Millisecond)
		ag.setChain(nil, nil)
	}()
	began := time.Now()
	_, err := ag.task(context.Background(), inv)
	if took := time.Since(began); took < 90*time.Millisecond {
		t.Fatalf("#task answered in %s, before the setup it should wait for", took)
	}
	if err == nil || !strings.Contains(err.Error(), "outfit did not grant") {
		t.Fatalf("after a late setup without a chain: err = %v, want the outfit refusal", err)
	}

	// Set up already: answered at once.
	began = time.Now()
	if _, err := ag.task(context.Background(), inv); err == nil || time.Since(began) > 50*time.Millisecond {
		t.Fatalf("set-up agent: err = %v after %s, want the outfit refusal at once", err, time.Since(began))
	}

	// Never set up: refused after readyWait, saying so.
	never := newAgent(context.Background(), nil, "ed:p", TaskOptions{}, nil)
	began = time.Now()
	_, err = never.task(context.Background(), inv)
	if err == nil || !strings.Contains(err.Error(), "not set up yet") || time.Since(began) < readyWait {
		t.Fatalf("never set up: err = %v after %s", err, time.Since(began))
	}
}
