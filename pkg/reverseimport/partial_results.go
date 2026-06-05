package reverseimport

import (
	"os"
	"path/filepath"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/genconfig"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport/job"
)

// orphanImportsManifestFile is the genconfig skip manifest sibling, surfaced
// as a reverse-import artifact so ui-core/reliable can inspect which import
// blocks were dropped (#732). Kept in sync with
// genconfig's orphanImportsFile constant (unexported there).
const orphanImportsManifestFile = "imports-skipped.json"

// skipEntry is one resource the engine could not import: dropped by genconfig
// (skipped) or by the iterative final-plan loop / first-import contract
// (failed). Preserves the identity so the per-resource ResourceResult carries
// the same address/type/region the user selected.
type skipEntry struct {
	identity   imported.ResourceIdentity
	status     job.ResourceStatus
	diagnostic job.Diagnostic
}

// skipTracker accumulates skipped/failed resources across the run, keyed by
// Terraform address so the same resource is never double-reported and a later
// failure attribution wins over an earlier skip.
type skipTracker struct {
	order   []string
	entries map[string]skipEntry
}

func newSkipTracker() *skipTracker {
	return &skipTracker{entries: make(map[string]skipEntry)}
}

func (s *skipTracker) put(e skipEntry) {
	addr := e.identity.Address
	if addr == "" {
		return
	}
	if _, ok := s.entries[addr]; !ok {
		s.order = append(s.order, addr)
	}
	s.entries[addr] = e
}

// addOrphanImports folds genconfig's skip manifest into the tracker. Each
// dropped import block becomes a ResourceStatusSkipped entry with the genconfig
// reason. The known map resolves the dropped Terraform address back to the rich
// identity the user selected; a manifest entry with no matching identity still
// produces a minimal identity from the address so it is not lost.
func (s *skipTracker) addOrphanImports(known map[string]imported.ResourceIdentity, skipped []genconfig.OrphanImport) {
	for _, o := range skipped {
		id, ok := known[o.Address]
		if !ok {
			id = imported.ResourceIdentity{Address: o.Address, ImportID: o.ImportID}
		}
		s.put(skipEntry{
			identity: id,
			status:   job.ResourceStatusSkipped,
			diagnostic: job.Diagnostic{
				Severity: "warning",
				Code:     "reverse_import_skipped_" + skipReasonCode(o.Reason),
				Field:    o.Address,
				Message:  skipReasonMessage(o),
			},
		})
	}
}

// addMissing records any identity that entered config generation but is absent
// from the surviving set and was not already tracked. This is the safety net
// for a drop that produced no manifest entry, so nothing disappears silently.
func (s *skipTracker) addMissing(pre map[string]imported.ResourceIdentity, surviving []imported.ImportedResource) {
	live := make(map[string]struct{}, len(surviving))
	for _, r := range surviving {
		live[r.Identity.Address] = struct{}{}
	}
	for addr, id := range pre {
		if _, stillThere := live[addr]; stillThere {
			continue
		}
		if _, tracked := s.entries[addr]; tracked {
			continue
		}
		s.put(skipEntry{
			identity: id,
			status:   job.ResourceStatusSkipped,
			diagnostic: job.Diagnostic{
				Severity: "warning",
				Code:     "reverse_import_skipped_no_generated_config",
				Field:    addr,
				Message:  "terraform config generation dropped this resource (no generated body)",
			},
		})
	}
}

// markFailed records a resource the final-plan/validate loop or the
// first-import contract attributed a failure to.
func (s *skipTracker) markFailed(id imported.ResourceIdentity, diag job.Diagnostic) {
	s.put(skipEntry{identity: id, status: job.ResourceStatusFailed, diagnostic: diag})
}

func (s *skipTracker) counts() (skipped, failed int) {
	for _, addr := range s.order {
		switch s.entries[addr].status {
		case job.ResourceStatusSkipped:
			skipped++
		case job.ResourceStatusFailed:
			failed++
		}
	}
	return skipped, failed
}

// identitiesByAddress indexes the resource set by Terraform address for
// attribution and identity recovery.
func identitiesByAddress(resources []imported.ImportedResource) map[string]imported.ResourceIdentity {
	out := make(map[string]imported.ResourceIdentity, len(resources))
	for _, r := range resources {
		if r.Identity.Address != "" {
			out[r.Identity.Address] = r.Identity
		}
	}
	return out
}

// dropResources returns resources with every address in failures removed,
// preserving order. The input slice is not mutated.
func dropResources(resources []imported.ImportedResource, failures []resourceFailure) []imported.ImportedResource {
	if len(failures) == 0 {
		return resources
	}
	drop := make(map[string]struct{}, len(failures))
	for _, f := range failures {
		drop[f.address] = struct{}{}
	}
	out := make([]imported.ImportedResource, 0, len(resources))
	for _, r := range resources {
		if _, gone := drop[r.Identity.Address]; gone {
			continue
		}
		out = append(out, r)
	}
	return out
}

// combinedResourceResults builds the per-resource Result.Resources[] slice:
// one ResourceStatusImported entry per surviving resource plus one
// skipped/failed entry per tracked drop. Imported entries carry their
// dependencies; skipped/failed entries carry the attributing diagnostic.
func combinedResourceResults(resources []imported.ImportedResource, dependenciesByAddress map[string][]imported.ResourceIdentity, skips *skipTracker) []job.ResourceResult {
	out := make([]job.ResourceResult, 0, len(resources)+len(skips.order))
	for _, r := range resources {
		rr := r
		out = append(out, job.ResourceResult{
			Identity:     r.Identity,
			Status:       job.ResourceStatusImported,
			Imported:     &rr,
			Dependencies: dependenciesByAddress[r.Identity.Address],
		})
	}
	for _, addr := range skips.order {
		e := skips.entries[addr]
		out = append(out, job.ResourceResult{
			Identity:    e.identity,
			Status:      e.status,
			Diagnostics: []job.Diagnostic{e.diagnostic},
		})
	}
	return out
}

// finalStatus computes the truthful job status (#732):
//   - all selected resources imported (no skips/failures) → succeeded
//   - at least one imported AND at least one skipped/failed → partial
//   - zero imported → failed
func finalStatus(importedCount int, skips *skipTracker) job.Status {
	skipped, failed := skips.counts()
	if importedCount == 0 {
		return job.StatusFailed
	}
	if skipped+failed > 0 {
		return job.StatusPartial
	}
	return job.StatusSucceeded
}

// addSkipManifestArtifact attaches genconfig's imports-skipped.json (when it
// exists in the genconfig workdir) to the result's debug artifacts so the
// dropped-imports manifest travels with the run.
func addSkipManifestArtifact(result *job.Result, workdir string) {
	if workdir == "" {
		return
	}
	path := filepath.Join(workdir, orphanImportsManifestFile)
	if _, err := os.Stat(path); err != nil {
		return
	}
	if a, err := artifact(path, "application/json"); err == nil {
		result.Artifacts.Debug = append(result.Artifacts.Debug, *a)
	}
}

func skipReasonCode(reason string) string {
	if reason == "" {
		return "no_generated_config"
	}
	return reason
}

func skipReasonMessage(o genconfig.OrphanImport) string {
	switch o.Reason {
	case "no_generated_config", "":
		return "terraform plan -generate-config-out produced no resource body for this import; dropped"
	default:
		return "import dropped during config generation (" + o.Reason + ")"
	}
}
