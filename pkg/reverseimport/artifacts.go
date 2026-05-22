package reverseimport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport/job"
)

const (
	importedJSONFile    = "imported.json"
	importedTFFile      = "imported.tf"
	importedProvidersTF = "providers-imported.tf"
	validateJSONFile    = "validate.json"
	tfplanJSONFile      = "tfplan.json"
	tfplanFile          = "tfplan.bin"
	planSummaryJSONFile = "plan-summary.json"
	reverseResultFile   = "reverse-result.json"
	graphJSONFile       = "graph.json"
)

func writeJSON(path string, v any) error {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filepath.Base(path), err)
	}
	body = append(body, '\n')
	return writeFileAtomic(path, body, 0o644)
}

func writeFileAtomic(path string, body []byte, perm os.FileMode) (rerr error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("open temp %s: %w", tmp, err)
	}
	defer func() {
		if rerr != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return fmt.Errorf("write temp %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("fsync temp %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

func artifact(path, mediaType string) (*job.Artifact, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	return &job.Artifact{
		Name:      filepath.Base(path),
		Path:      path,
		MediaType: mediaType,
		SHA256:    hex.EncodeToString(sum[:]),
		SizeBytes: info.Size(),
	}, nil
}

func addArtifact(ptr **job.Artifact, path, mediaType string) error {
	a, err := artifact(path, mediaType)
	if err != nil {
		return err
	}
	*ptr = a
	return nil
}
