package imported

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// maxLabelLen caps the normalized label portion of a generated address. The
// reserved suffix budget covers `_<8hex>` collision tags plus an optional
// numeric counter. Terraform itself does not impose a hard label cap, but a
// conservative limit keeps composed HCL readable and stays well under any
// downstream tool's column limits.
const (
	maxLabelLen     = 96
	hashSuffixLen   = 8
	suffixReserve   = hashSuffixLen + 4 // "_<8hex>" plus headroom for "_NN"
	collisionPrefix = "r_"
)

// GenerateAddress returns a deterministic Terraform address
// (`<type>.<label>`) for the given identity. The exists predicate is consulted
// to avoid collisions; callers seed it with both currently-used and retired
// addresses so previously-claimed names are never reused unless reclaiming the
// same canonical identity.
//
// Algorithm (per docs/managed-resource-tiers.md lines 528-544):
//
//  1. Pick the first non-empty name hint from id.NameHint, NativeIDs["name"],
//     final segments of NativeIDs["arn"] / NativeIDs["self_link"], final
//     segment of id.ImportID, then the resource type stem.
//  2. Normalize: lowercase ASCII, replace [^a-z0-9_] with `_`, collapse
//     repeated `_`, trim leading/trailing `_`, prefix `r_` if it does not
//     start with a letter, cap to maxLabelLen-suffixReserve.
//  3. Compose addr = `<type>.<normalized>`. If exists is nil or returns
//     false, return.
//  4. Otherwise append `_<8hex>` of the canonical identity hash.
//  5. If still colliding, append a numeric counter `_2`, `_3`, ...
func GenerateAddress(id ResourceIdentity, exists func(addr string) bool) string {
	hash := identityHash(id)

	hint := pickNameHint(id, hash)
	label := normalizeLabel(hint, maxLabelLen-suffixReserve)
	if label == "" {
		// Defensive: normalize emptied the hint entirely. Fall back to a
		// hash-only label so we never emit a bare `<type>.` address.
		label = collisionPrefix + hash[:hashSuffixLen]
	}

	addr := id.Type + "." + label
	if exists == nil || !exists(addr) {
		return addr
	}

	hashed := addr + "_" + hash[:hashSuffixLen]
	if !exists(hashed) {
		return hashed
	}

	for n := 2; ; n++ {
		candidate := hashed + "_" + strconv.Itoa(n)
		if !exists(candidate) {
			return candidate
		}
	}
}

// pickNameHint chooses the first non-empty name source per the design doc's
// precedence order. hashFallback is used only if every source is empty.
func pickNameHint(id ResourceIdentity, hashFallback string) string {
	if h := strings.TrimSpace(id.NameHint); h != "" {
		return h
	}
	if h := strings.TrimSpace(id.NativeIDs["name"]); h != "" {
		return h
	}
	if h := lastSegment(id.NativeIDs["arn"]); h != "" {
		return h
	}
	if h := lastSegment(id.NativeIDs["self_link"]); h != "" {
		return h
	}
	if h := lastSegment(id.ImportID); h != "" {
		return h
	}
	if h := typeStem(id.Type); h != "" {
		return h
	}
	if hashFallback == "" {
		return ""
	}
	return collisionPrefix + hashFallback[:hashSuffixLen]
}

// lastSegment returns the substring after the final `/`, `:`, or `,`.
// Empty input returns empty.
func lastSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, sep := range []string{"/", ":", ","} {
		if i := strings.LastIndex(s, sep); i >= 0 && i < len(s)-1 {
			s = s[i+1:]
		}
	}
	return s
}

// typeStem returns the suffix of a Terraform type after the cloud prefix:
// "aws_sqs_queue" -> "sqs_queue", "google_compute_instance" -> "compute_instance".
// Returns the unchanged type if no recognized prefix is present.
func typeStem(t string) string {
	t = strings.TrimSpace(t)
	switch {
	case strings.HasPrefix(t, "aws_"):
		return t[len("aws_"):]
	case strings.HasPrefix(t, "google_"):
		return t[len("google_"):]
	case strings.HasPrefix(t, "gcp_"):
		return t[len("gcp_"):]
	}
	return t
}

// normalizeLabel converts an arbitrary hint into a Terraform-safe identifier.
// Behavior:
//   - lowercase ASCII; non-ASCII letters are dropped (replaced with `_`).
//   - any rune outside [a-z0-9_] becomes `_`.
//   - repeated `_` collapse to a single `_`.
//   - leading and trailing `_` trimmed.
//   - if the first rune is not [a-z], prefix `r_`.
//   - truncated to maxLen runes (after all other steps), trimming trailing
//     `_` again so the cap never produces `foo_`.
func normalizeLabel(hint string, maxLen int) string {
	hint = strings.ToLower(strings.TrimSpace(hint))
	if hint == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(hint))
	prevUnderscore := false
	for _, r := range hint {
		isAlnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		isUnderscore := r == '_'
		if !isAlnum && !isUnderscore {
			// non-alnum collapses into a single `_`.
			if !prevUnderscore {
				b.WriteByte('_')
				prevUnderscore = true
			}
			continue
		}
		if isUnderscore && prevUnderscore {
			// Collapse consecutive underscores from any source.
			continue
		}
		b.WriteRune(r)
		prevUnderscore = isUnderscore
	}

	out := strings.Trim(b.String(), "_")
	if out == "" {
		return ""
	}

	if c := out[0]; !(c >= 'a' && c <= 'z') {
		out = collisionPrefix + out
	}

	if maxLen > 0 && len(out) > maxLen {
		out = out[:maxLen]
		out = strings.TrimRight(out, "_")
	}
	return out
}

// identityHash returns the lowercase hex sha256 of a canonical, deterministic
// rendering of the identity's correlation tuple. ProviderIdentity map keys
// are sorted before hashing because Go map iteration is unordered — without
// sorting, the same identity would hash to different values across runs.
func identityHash(id ResourceIdentity) string {
	var b strings.Builder
	b.WriteString(id.Cloud)
	b.WriteByte('|')
	b.WriteString(id.AccountID)
	b.WriteByte('|')
	b.WriteString(id.ProjectID)
	b.WriteByte('|')
	b.WriteString(id.Region)
	b.WriteByte('|')
	b.WriteString(id.Location)
	b.WriteByte('|')
	b.WriteString(id.Type)
	b.WriteByte('|')
	b.WriteString(id.ImportID)
	b.WriteByte('|')

	keys := make([]string, 0, len(id.ProviderIdentity))
	for k := range id.ProviderIdentity {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(id.ProviderIdentity[k])
		b.WriteByte(';')
	}

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
