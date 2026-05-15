// Package snapshot is the sealed-envelope serialization layer for a
// slice of imported resources as it rides inside the downstream
// stack_versions.imported column.
//
// The envelope format is intentionally minimal:
//
//	{"version": 1, "resources": [<ImportedResource>, ...]}
//
// The bare slice form ([{...}, {...}]) is still accepted on decode for
// backward compatibility with v0 callers — the legacy storage shape
// used by the first cut of #144 — but every emit goes through the
// versioned envelope.
//
// Byte-stable output: MarshalSnapshot sorts the input by
// Identity.Address before serializing so the same logical slice
// produces an identical byte stream on every call. Downstream change
// detection in stack_versions compares envelopes for equality; non-
// deterministic field ordering would manufacture spurious "imported
// changed" rows.
package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	composerimported "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// CurrentVersion is the envelope version this package emits and the
// highest version it can decode. Bump when adding fields that older
// readers cannot ignore (e.g. a top-level field whose absence changes
// semantics, not a per-resource additive field that round-trips through
// json.RawMessage). Additive struct fields on ImportedResource itself
// do NOT require a version bump — they ride along inside the
// "resources" array via encoding/json's standard struct decode.
const CurrentVersion = 1

// ErrUnsupportedVersion is returned by UnmarshalSnapshot for an
// envelope whose version is higher than CurrentVersion. The error
// payload includes the offending version so the caller can log it
// without re-decoding the envelope.
var ErrUnsupportedVersion = errors.New("snapshot: unsupported envelope version")

// envelopeV1 is the wire shape of a v1 snapshot. The version field is
// always populated on emit; the resources slice is the
// Address-sorted list.
type envelopeV1 struct {
	Version   int                                 `json:"version"`
	Resources []composerimported.ImportedResource `json:"resources"`
}

// MarshalSnapshot serializes a slice of imported resources into the
// sealed envelope format consumed by stack_versions.imported in the
// downstream backend. The returned byte slice round-trips through
// UnmarshalSnapshot to the original irs (modulo Address-sorted order,
// which is intentional). The returned version is always CurrentVersion;
// it's surfaced explicitly so callers don't have to re-decode the
// envelope to learn the version they just emitted.
func MarshalSnapshot(
	irs []composerimported.ImportedResource,
) ([]byte, int, error) {
	// Defensive copy so we don't mutate the caller's slice ordering.
	// nil-in / nil-out is preserved as len-0 — both round-trip to the
	// same envelope.
	sorted := make([]composerimported.ImportedResource, len(irs))
	copy(sorted, irs)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Identity.Address < sorted[j].Identity.Address
	})

	env := envelopeV1{
		Version:   CurrentVersion,
		Resources: sorted,
	}
	out, err := json.Marshal(env)
	if err != nil {
		return nil, 0, fmt.Errorf("snapshot: marshal envelope: %w", err)
	}
	return out, CurrentVersion, nil
}

// UnmarshalSnapshot reverses MarshalSnapshot. Returns
// ErrUnsupportedVersion (wrapped via fmt.Errorf) when version >
// CurrentVersion. A v0 envelope (no version byte — the legacy JSON
// array of ImportedResource) is treated as version 0 for backward
// compatibility; the returned version is 0 in that case.
func UnmarshalSnapshot(
	raw []byte,
) ([]composerimported.ImportedResource, int, error) {
	if len(raw) == 0 {
		return nil, 0, nil
	}

	// Sniff array-vs-object by skipping leading whitespace and
	// peeking the first non-space byte. encoding/json accepts the
	// same forms, so this matches the decode surface exactly.
	first := firstNonSpaceByte(raw)
	switch first {
	case '[':
		var legacy []composerimported.ImportedResource
		if err := json.Unmarshal(raw, &legacy); err != nil {
			return nil, 0, fmt.Errorf("snapshot: decode v0 array: %w", err)
		}
		return legacy, 0, nil
	case '{':
		// Decode just the version first so we can fail fast on an
		// unsupported envelope before paying the cost of decoding
		// every resource into the typed struct shape.
		var probe struct {
			Version int `json:"version"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, 0, fmt.Errorf("snapshot: decode envelope version: %w", err)
		}
		if probe.Version > CurrentVersion {
			return nil, probe.Version, fmt.Errorf("%w: got %d, max %d",
				ErrUnsupportedVersion, probe.Version, CurrentVersion)
		}
		var env envelopeV1
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, probe.Version, fmt.Errorf("snapshot: decode envelope: %w", err)
		}
		return env.Resources, env.Version, nil
	default:
		return nil, 0, fmt.Errorf("snapshot: unexpected leading byte %q", first)
	}
}

// firstNonSpaceByte returns the first non-whitespace byte from raw, or
// 0 if raw is all whitespace. Whitespace per the JSON spec: space,
// tab, CR, LF.
func firstNonSpaceByte(raw []byte) byte {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return b
		}
	}
	return 0
}
