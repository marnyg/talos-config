// Command workouts serves the tracker and runs its admin subcommands.
//
//	workouts serve  -data /var/lib/workouts [-listen :8080]
//	workouts import -data /var/lib/workouts <export-dir>   # Strong export
//
// The data dir holds workouts.db (SQLite, WAL) and media/.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/marnyg/talos-config/workouts/store"
	"github.com/marnyg/talos-config/workouts/strongimport"
	"github.com/marnyg/talos-config/workouts/web"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case "import":
		doImport(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: workouts serve|import [flags]")
	os.Exit(2)
}

func openStore(data string) (*store.Store, string) {
	if err := os.MkdirAll(filepath.Join(data, "media"), 0o755); err != nil {
		log.Fatal(err)
	}
	s, err := store.Open(filepath.Join(data, "workouts.db"))
	if err != nil {
		log.Fatal(err)
	}
	return s, filepath.Join(data, "media")
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	data := fs.String("data", "/var/lib/workouts", "data directory")
	listen := fs.String("listen", ":8080", "listen address")
	tz := fs.String("tz", "Europe/Oslo", "display time zone")
	fs.Parse(args)
	s, media := openStore(*data)
	defer s.Close()
	loc, err := time.LoadLocation(*tz)
	if err != nil {
		log.Fatal(err)
	}
	h, err := web.New(s, media, loc)
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{Addr: *listen, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Printf("workouts: serving on %s, data %s", *listen, *data)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func doImport(args []string) {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	data := fs.String("data", "/var/lib/workouts", "data directory")
	noMedia := fs.Bool("no-media", false, "do not copy media files")
	fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: workouts import -data DIR <export-dir>")
		os.Exit(2)
	}
	s, media := openStore(*data)
	defer s.Close()
	if *noMedia {
		media = ""
	}
	r, err := strongimport.Import(context.Background(), s, fs.Arg(0), strongimport.Options{MediaDir: media, Log: os.Stderr})
	if err != nil {
		log.Fatal(err)
	}
	for _, sk := range r.Skipped {
		fmt.Fprintln(os.Stderr, "skipped:", sk)
	}
}
