package policygen

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// registryTypesJSON is the scan of each pinned Terraform registry module the
// presets call (aws/eks and aws/vpc call terraform-aws-modules), flattened
// with the registry modules those call in turn.
//
// Why it is checked in: the composer embeds only this repo's .tf files, and
// the registry modules are fetched at terraform init. Scanning them in every
// go test would need the network. `go run ./cmd/policygen registry` makes
// this file; TestRegistryTypesCurrent fails when a preset pins a registry
// module version that has no entry, so a version bump forces a rescan.
//
//go:embed registry_types.json
var registryTypesJSON []byte

// RegistryScans returns the checked-in registry module scans, keyed
// "namespace/name/provider@version".
func RegistryScans() (map[string]ModuleScan, error) {
	out := map[string]ModuleScan{}
	if err := json.Unmarshal(registryTypesJSON, &out); err != nil {
		return nil, fmt.Errorf("parse registry_types.json: %w", err)
	}
	return out, nil
}

var exactVersionRE = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// ScanRegistryModule downloads a registry module at an exact version (with
// git) and scans it, following the registry modules it calls. It needs the
// network and git, so only `policygen registry` calls it, never a test.
func ScanRegistryModule(key string) (ModuleScan, error) {
	a := newAcc()
	if err := scanRegistry(key, a, map[string]bool{}); err != nil {
		return ModuleScan{}, err
	}
	return a.result(), nil
}

func scanRegistry(key string, a *acc, seen map[string]bool) error {
	if seen[key] {
		return nil
	}
	seen[key] = true
	src, ver, _ := strings.Cut(key, "@")
	if !exactVersionRE.MatchString(ver) {
		// A range would make the scan differ from what terraform init
		// resolves on another day. The presets pin exact versions for
		// the same reason (see aws/eks/main.tf).
		return fmt.Errorf("registry module %s: version %q is not an exact x.y.z pin", src, ver)
	}
	parts := strings.Split(src, "/")
	if len(parts) != 3 || parts[0] != "terraform-aws-modules" || parts[2] != "aws" {
		return fmt.Errorf("registry module %s: only terraform-aws-modules/<name>/aws is supported; add its git URL rule here", src)
	}
	url := "https://github.com/terraform-aws-modules/terraform-aws-" + parts[1] + ".git"
	dir, err := os.MkdirTemp("", "policygen-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()                                                      // best effort: a leftover temp clone does no harm
	cmd := exec.Command("git", "clone", "--quiet", "--depth", "1", "--branch", "v"+ver, url, dir) //nolint:gosec // url and tag are built from a validated registry key
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("clone %s v%s: %v: %s", url, ver, err, out)
	}
	s, err := ScanModule(os.DirFS(dir), ".")
	if err != nil {
		return fmt.Errorf("scan %s: %w", key, err)
	}
	a.merge(s)
	for _, nested := range s.RegistrySources {
		if err := scanRegistry(nested, a, seen); err != nil {
			return err
		}
	}
	return nil
}
