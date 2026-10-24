// Command tasks serves the task app: an event log in SQLite, component
// tables rebuilt in memory at boot, and the registered systems' views.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/marnyg/talos-config/tasks/ecs"
	"github.com/marnyg/talos-config/tasks/systems/core"
	"github.com/marnyg/talos-config/tasks/systems/dag"
	"github.com/marnyg/talos-config/tasks/systems/triage"
	"github.com/marnyg/talos-config/tasks/web"
)

func main() {
	var (
		data   = flag.String("data", "./data", "data directory (holds tasks.db)")
		listen = flag.String("listen", ":8080", "listen address")
		tz     = flag.String("tz", "", "local time zone for day boundaries (default: system)")
	)
	flag.Parse()

	if *tz != "" {
		loc, err := time.LoadLocation(*tz)
		if err != nil {
			log.Fatalf("tz: %v", err)
		}
		time.Local = loc
	}
	if err := os.MkdirAll(*data, 0o755); err != nil {
		log.Fatal(err)
	}
	logStore, err := ecs.OpenSQLite(filepath.Join(*data, "tasks.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer logStore.Close()

	systems := []ecs.System{core.System{}, dag.System{}, triage.System{}}
	world, err := ecs.New(logStore, systems...)
	if err != nil {
		log.Fatalf("rebuild: %v", err)
	}

	mux := http.NewServeMux()
	for _, s := range systems {
		if m, ok := s.(web.Mounter); ok {
			m.Mount(mux, world)
		}
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	log.Printf("tasks: listening on %s, data in %s", *listen, *data)
	log.Fatal(http.ListenAndServe(*listen, mux))
}
