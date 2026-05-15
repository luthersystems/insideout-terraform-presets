package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/api/googleapi"
	loggingv2 "google.golang.org/api/logging/v2"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// loggingProjectSinkEnricher implements AttributeEnricher AND
// ByIDEnricher for google_logging_project_sink. Pairs with
// loggingProjectSinkDiscoverer.
//
// Sinks.Get takes a single fully-qualified resource name of the form
// `projects/<p>/sinks/<n>`. The discoverer stores only the short sink
// name in NameHint / ImportID (the provider import shape), so the
// enricher constructs the full name from c.ProjectID + name at call
// time.
//
// Mapping rationale: writer_identity is computed-only in the TF schema
// but is structurally a real identity the user needs to grant IAM
// roles to, so it is populated for visibility. unique_writer_identity
// is not a returned API field — the provider uses it as an input
// signal only — so it is left nil on Get.
//
// Computed-only fields skipped per decision #5: id, project.
type loggingProjectSinkEnricher struct {
	fetch func(ctx context.Context, svc *loggingv2.Service, sinkName string) (*loggingv2.LogSink, error)
}

func newLoggingProjectSinkEnricher() AttributeEnricher {
	return &loggingProjectSinkEnricher{fetch: defaultLoggingProjectSinkFetch}
}

var (
	_ AttributeEnricher = (*loggingProjectSinkEnricher)(nil)
	_ ByIDEnricher      = (*loggingProjectSinkEnricher)(nil)
)

func (loggingProjectSinkEnricher) ResourceType() string { return loggingProjectSinkTFType }

func (e loggingProjectSinkEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchTyped(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

func (e loggingProjectSinkEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("logging_project_sink: nil identity")
	}
	return e.fetchTyped(ctx, identity, c)
}

func (e loggingProjectSinkEnricher) fetchTyped(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.Logging == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if c.ProjectID == "" {
		return nil, fmt.Errorf("logging_project_sink: EnrichClients.ProjectID required to construct sinkName")
	}
	name := loggingProjectSinkNameForEnrich(id)
	if name == "" {
		return nil, fmt.Errorf("logging_project_sink: cannot derive sink name from Identity (Address=%q ImportID=%q NameHint=%q)",
			id.Address, id.ImportID, id.NameHint)
	}
	fullName := fmt.Sprintf("projects/%s/sinks/%s", c.ProjectID, name)
	s, err := e.fetch(ctx, c.Logging, fullName)
	if err != nil {
		if isLoggingNotFound(err) {
			return nil, fmt.Errorf("logging_project_sink: %s: %w", fullName, ErrNotFound)
		}
		return nil, fmt.Errorf("logging_project_sink: get %s: %w", fullName, err)
	}
	typed := mapLoggingProjectSink(s)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("logging_project_sink: marshal Attrs: %w", err)
	}
	return raw, nil
}

// loggingProjectSinkNameForEnrich pulls the short sink name from the
// Identity. Precedence: NameHint, ImportID (which is the sink short
// name for this resource per provider docs).
func loggingProjectSinkNameForEnrich(id *imported.ResourceIdentity) string {
	if id.NameHint != "" {
		return id.NameHint
	}
	return id.ImportID
}

func defaultLoggingProjectSinkFetch(ctx context.Context, svc *loggingv2.Service, sinkName string) (*loggingv2.LogSink, error) {
	return svc.Sinks.Get(sinkName).Context(ctx).Do()
}

func isLoggingNotFound(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusNotFound
	}
	return false
}

// mapLoggingProjectSink converts a *loggingv2.LogSink into the typed
// Layer-1 *generated.GoogleLoggingProjectSink model.
func mapLoggingProjectSink(s *loggingv2.LogSink) *generated.GoogleLoggingProjectSink {
	out := &generated.GoogleLoggingProjectSink{}
	if s.Name != "" {
		out.Name = generated.LiteralOf(s.Name)
	}
	if s.Destination != "" {
		out.Destination = generated.LiteralOf(s.Destination)
	}
	if s.Filter != "" {
		out.Filter = generated.LiteralOf(s.Filter)
	}
	if s.Description != "" {
		out.Description = generated.LiteralOf(s.Description)
	}
	if s.Disabled {
		out.Disabled = generated.LiteralOf(true)
	}
	if s.WriterIdentity != "" {
		out.WriterIdentity = generated.LiteralOf(s.WriterIdentity)
	}
	if s.BigqueryOptions != nil {
		opts := generated.GoogleLoggingProjectSinkBigqueryOptions{}
		if s.BigqueryOptions.UsePartitionedTables {
			opts.UsePartitionedTables = generated.LiteralOf(true)
		}
		out.BigqueryOptions = []generated.GoogleLoggingProjectSinkBigqueryOptions{opts}
	}
	if len(s.Exclusions) > 0 {
		exclusions := make([]generated.GoogleLoggingProjectSinkExclusions, 0, len(s.Exclusions))
		for _, x := range s.Exclusions {
			if x == nil {
				continue
			}
			ex := generated.GoogleLoggingProjectSinkExclusions{}
			if x.Name != "" {
				ex.Name = generated.LiteralOf(x.Name)
			}
			if x.Filter != "" {
				ex.Filter = generated.LiteralOf(x.Filter)
			}
			if x.Description != "" {
				ex.Description = generated.LiteralOf(x.Description)
			}
			if x.Disabled {
				ex.Disabled = generated.LiteralOf(true)
			}
			exclusions = append(exclusions, ex)
		}
		if len(exclusions) > 0 {
			out.Exclusions = exclusions
		}
	}
	return out
}
