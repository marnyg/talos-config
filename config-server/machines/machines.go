// Package machines loads the declared machine set — talos/machines/
// <mac>/meta.yaml directories — and composes Talos machine configs
// from it with the machinery strategic-merge patcher, the same code
// path as `talosctl machineconfig patch`.
package machines

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"
	"gopkg.in/yaml.v3"
)

// Machine is the parsed form of talos/machines/<mac>/meta.yaml.
type Machine struct {
	Config  string   `yaml:"config"`
	Patches []string `yaml:"patches"`
	// Name is the machine's mesh name (<name>.<zone>, the member cert's
	// name caveat); defaults to the MAC with dashes. See DNSName.
	Name string `yaml:"name"`
	// UUID is the node's SMBIOS UUID (shown on /status at approval).
	// It is the durable KMS unseal allowlist: deleting it revokes.
	UUID string `yaml:"uuid"`
	// DiskEncryption injects systemDiskEncryption (KMS + recovery
	// passphrase) into the served config. Takes effect at install
	// time only — an existing machine needs a wipe to encrypt.
	DiskEncryption bool `yaml:"diskEncryption"`

	// Dir is the machines/<mac> directory, absolute (set by Load).
	Dir string `yaml:"-"`
}

// NormalizeMAC lowercases and converts dashes to colons.
func NormalizeMAC(mac string) string {
	return strings.ToLower(strings.ReplaceAll(mac, "-", ":"))
}

// DNSName returns the machine's mesh label — the meta.yaml name if
// set, else the MAC with dashes. It is the member cert's name caveat,
// the <name>.<zone> apid certSAN and the /status DNS column: one
// source for the name, so re-keying or re-enrolling never moves a
// member (invariant 1).
func DNSName(mac string, m Machine) string {
	if m.Name != "" {
		return strings.ToLower(strings.TrimSpace(m.Name))
	}
	return strings.ReplaceAll(mac, ":", "-")
}

// Load scans machinesDir for <mac>/meta.yaml, returns MAC → Machine.
func Load(machinesDir string) (map[string]Machine, error) {
	entries, err := os.ReadDir(machinesDir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", machinesDir, err)
	}

	byMAC := make(map[string]Machine)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(machinesDir, e.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "meta.yaml"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading meta for %s: %w", e.Name(), err)
		}

		var m Machine
		if err := yaml.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("parsing %s/meta.yaml: %w", e.Name(), err)
		}
		m.Dir = dir
		byMAC[NormalizeMAC(e.Name())] = m
	}
	return byMAC, nil
}

// BuildConfig composes the base config with all patches for the
// machine, plus any literal extra patches (applied last; used for
// serve-time mesh/disk-key injection so key material never hits disk).
func BuildConfig(root string, m Machine, extra ...string) ([]byte, error) {
	base, err := os.ReadFile(filepath.Join(root, m.Config))
	if err != nil {
		return nil, fmt.Errorf("reading base config: %w", err)
	}

	patchRefs := make([]string, 0, len(m.Patches)+len(extra)+1)
	for _, p := range m.Patches {
		patchRefs = append(patchRefs, "@"+filepath.Join(root, p))
	}
	if machinePatch := filepath.Join(m.Dir, "patch.yaml"); fileExists(machinePatch) {
		patchRefs = append(patchRefs, "@"+machinePatch)
	}
	patchRefs = append(patchRefs, extra...)

	patches, err := configpatcher.LoadPatches(patchRefs)
	if err != nil {
		return nil, fmt.Errorf("loading patches: %w", err)
	}

	out, err := configpatcher.Apply(configpatcher.WithBytes(base), patches)
	if err != nil {
		return nil, fmt.Errorf("applying patches: %w", err)
	}
	return out.Bytes()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
