//go:build ignore

// oscheck: type-check a Go package for a GOOS that is not the host's,
// without compiling anything.
//
//	cd config-server && go run ../scripts/oscheck.go linux ./cmd/irohup ./fakeip ./mobile
//
// Why this exists: packages that split files by OS build constraint
// (cmd/irohup's tun.go = darwin, tun_other.go = !darwin) only get the
// host's half compiled by `go build`/`go vet`; the other half rots
// unnoticed until the linux hub image build fails (2026-09-21:
// tun_other.go had referred to a type deleted in c075081 for a day,
// and the fly deploy was the first thing to notice). The obvious fix —
// `GOOS=linux go vet` — needs a linux C toolchain because iroh-go is
// cgo, and CGO_ENABLED=0 excludes iroh-go outright. This uses go/types
// with the *source* importer instead: dependencies are type-checked
// from source under the target GOOS's build tags, and cgo packages are
// handled by `go tool cgo`'s preamble pass with the host compiler
// (headers are platform-neutral). ~20–30 s per package, no
// cross-toolchain. Tags: always "iroh" (the only custom tag in the
// tree). Called by .githooks/pre-push.
package main

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: oscheck <goos> <pkgdir>...")
		os.Exit(2)
	}
	goos := os.Args[1]
	ctxt := build.Default
	ctxt.GOOS = goos
	ctxt.CgoEnabled = true
	ctxt.BuildTags = []string{"iroh"}
	build.Default = ctxt // the source importer reads build.Default

	fset := token.NewFileSet()
	failed := false
	for _, dir := range os.Args[2:] {
		if err := check(&ctxt, fset, goos, dir); err != nil {
			fmt.Fprintf(os.Stderr, "oscheck %s %s: %v\n", goos, dir, err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func check(ctxt *build.Context, fset *token.FileSet, goos, dir string) error {
	bp, err := ctxt.ImportDir(dir, 0)
	if err != nil {
		if _, ok := err.(*build.NoGoError); ok {
			fmt.Printf("oscheck %s %s: no Go files for this GOOS, skipped\n", goos, dir)
			return nil
		}
		return err
	}
	var files []*ast.File
	for _, f := range bp.GoFiles {
		af, err := parser.ParseFile(fset, filepath.Join(dir, f), nil, 0)
		if err != nil {
			return err
		}
		files = append(files, af)
	}
	n := 0
	conf := types.Config{
		Importer: importer.ForCompiler(fset, "source", nil),
		Error: func(e error) {
			n++
			fmt.Fprintln(os.Stderr, e)
		},
	}
	if _, err := conf.Check(bp.ImportPath, fset, files, nil); err != nil {
		return fmt.Errorf("%d type error(s)", n)
	}
	fmt.Printf("oscheck %s %s: ok (%d files)\n", goos, dir, len(bp.GoFiles))
	return nil
}
