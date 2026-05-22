package job

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// DecodeRequest reads a reverse-import request from r.
//
// The preferred shape is Request. For local CLI troubleshooting, this also
// accepts the historical imported.json shape ([]ImportedResource) and converts
// it into a Request using each resource's identity/tier/source.
func DecodeRequest(r io.Reader) (Request, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return Request{}, err
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return Request{}, fmt.Errorf("reverse import request cannot be null")
	}

	var req Request
	if err := json.Unmarshal(raw, &req); err == nil && (req.Version != 0 || len(req.Resources) > 0) {
		if req.Version == 0 {
			req.Version = Version
		}
		return req, nil
	}

	var resources []imported.ImportedResource
	if err := json.Unmarshal(raw, &resources); err != nil {
		return Request{}, fmt.Errorf("decode reverse import request: %w", err)
	}
	req = Request{
		Version:   Version,
		Resources: make([]ResourceSpec, 0, len(resources)),
	}
	for _, r := range resources {
		req.Resources = append(req.Resources, ResourceSpec{
			Identity: r.Identity,
			Tier:     r.Tier,
			Source:   r.Source,
		})
	}
	return req, nil
}

// ImportedResources returns the request resources as ImportedResource carriers,
// filling in the default reverse-import tier/source when omitted.
func (r Request) ImportedResources() []imported.ImportedResource {
	out := make([]imported.ImportedResource, 0, len(r.Resources))
	for _, spec := range r.Resources {
		tier := spec.Tier
		if tier == "" {
			tier = imported.TierImportedFlat
		}
		source := spec.Source
		if source == "" {
			source = imported.SourceImporter
		}
		out = append(out, imported.ImportedResource{
			Identity: spec.Identity,
			Tier:     tier,
			Source:   source,
		})
	}
	return out
}
