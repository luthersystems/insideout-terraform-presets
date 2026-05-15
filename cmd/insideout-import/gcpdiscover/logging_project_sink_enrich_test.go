package gcpdiscover

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
	loggingv2 "google.golang.org/api/logging/v2"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

func TestLoggingProjectSinkEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "google_logging_project_sink", newLoggingProjectSinkEnricher().ResourceType())
}

func TestLoggingProjectSinkEnricher_NilClient_ReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newLoggingProjectSinkEnricher()
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: loggingProjectSinkTFType, NameHint: "errors-sink"},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Logging: nil, ProjectID: "p"})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Empty(t, ir.Attrs)
}

func TestLoggingProjectSinkEnricher_ProjectIDRequired(t *testing.T) {
	t.Parallel()
	e := &loggingProjectSinkEnricher{
		fetch: func(_ context.Context, _ *loggingv2.Service, _ string) (*loggingv2.LogSink, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: loggingProjectSinkTFType, NameHint: "errors-sink"},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Logging: &loggingv2.Service{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ProjectID required")
}

func TestLoggingProjectSinkEnricher_NotFound_ReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	e := &loggingProjectSinkEnricher{
		fetch: func(_ context.Context, _ *loggingv2.Service, _ string) (*loggingv2.LogSink, error) {
			return nil, &googleapi.Error{Code: http.StatusNotFound, Message: "not found"}
		},
	}
	id := &imported.ResourceIdentity{Type: loggingProjectSinkTFType, NameHint: "errors-sink"}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{Logging: &loggingv2.Service{}, ProjectID: "p"})
	require.ErrorIs(t, err, ErrNotFound)
	assert.Nil(t, raw)
}

func TestLoggingProjectSinkEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	sink := &loggingv2.LogSink{
		Name:           "errors-sink",
		Destination:    "storage.googleapis.com/my-bucket",
		Filter:         "severity>=ERROR",
		Description:    "All errors to GCS",
		Disabled:       false,
		WriterIdentity: "serviceAccount:p-12345@gcp-sa-logging.iam.gserviceaccount.com",
		Exclusions: []*loggingv2.LogExclusion{
			{Name: "no-debug", Filter: "severity=DEBUG", Disabled: true, Description: "drop debugs"},
		},
		BigqueryOptions: &loggingv2.BigQueryOptions{UsePartitionedTables: true},
	}
	var gotFullName string
	e := &loggingProjectSinkEnricher{
		fetch: func(_ context.Context, _ *loggingv2.Service, n string) (*loggingv2.LogSink, error) {
			gotFullName = n
			return sink, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: loggingProjectSinkTFType, NameHint: "errors-sink"},
	}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{Logging: &loggingv2.Service{}, ProjectID: "my-project"}))
	assert.Equal(t, "projects/my-project/sinks/errors-sink", gotFullName)

	decoded, err := generated.UnmarshalAttrs("google_logging_project_sink", ir.Attrs)
	require.NoError(t, err)
	ls, ok := decoded.(*generated.GoogleLoggingProjectSink)
	require.True(t, ok)
	require.NotNil(t, ls.Name)
	assert.Equal(t, "errors-sink", *ls.Name.Literal)
	require.NotNil(t, ls.Destination)
	assert.Contains(t, *ls.Destination.Literal, "storage.googleapis.com/my-bucket")
	require.NotNil(t, ls.Filter)
	assert.Equal(t, "severity>=ERROR", *ls.Filter.Literal)
	require.NotNil(t, ls.WriterIdentity)
	assert.Contains(t, *ls.WriterIdentity.Literal, "gserviceaccount")
	require.Len(t, ls.Exclusions, 1)
	require.NotNil(t, ls.Exclusions[0].Disabled)
	assert.True(t, *ls.Exclusions[0].Disabled.Literal)
	require.Len(t, ls.BigqueryOptions, 1)
	require.NotNil(t, ls.BigqueryOptions[0].UsePartitionedTables)
	assert.True(t, *ls.BigqueryOptions[0].UsePartitionedTables.Literal)
}

func TestLoggingProjectSinkEnricher_EnrichByID_MirrorsEnrich(t *testing.T) {
	t.Parallel()
	sink := &loggingv2.LogSink{Name: "errors-sink", Destination: "gs", Filter: "x"}
	mkFetch := func() func(context.Context, *loggingv2.Service, string) (*loggingv2.LogSink, error) {
		return func(_ context.Context, _ *loggingv2.Service, _ string) (*loggingv2.LogSink, error) {
			return sink, nil
		}
	}
	enrichE := &loggingProjectSinkEnricher{fetch: mkFetch()}
	byIDE := &loggingProjectSinkEnricher{fetch: mkFetch()}

	id := imported.ResourceIdentity{Type: loggingProjectSinkTFType, NameHint: "errors-sink"}
	ir := &imported.ImportedResource{Identity: id}
	require.NoError(t, enrichE.Enrich(context.Background(), ir, EnrichClients{Logging: &loggingv2.Service{}, ProjectID: "p"}))

	raw, err := byIDE.EnrichByID(context.Background(), &id, EnrichClients{Logging: &loggingv2.Service{}, ProjectID: "p"})
	require.NoError(t, err)
	assert.JSONEq(t, string(ir.Attrs), string(raw))
}

func TestLoggingProjectSinkEnricher_NonNotFoundErrorPassesThrough(t *testing.T) {
	t.Parallel()
	upstream := &googleapi.Error{Code: http.StatusForbidden, Message: "denied"}
	e := &loggingProjectSinkEnricher{
		fetch: func(_ context.Context, _ *loggingv2.Service, _ string) (*loggingv2.LogSink, error) {
			return nil, upstream
		},
	}
	id := &imported.ResourceIdentity{Type: loggingProjectSinkTFType, NameHint: "n"}
	_, err := e.EnrichByID(context.Background(), id, EnrichClients{Logging: &loggingv2.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
	var gerr *googleapi.Error
	require.True(t, errors.As(err, &gerr))
	assert.Equal(t, http.StatusForbidden, gerr.Code)
}

func TestLoggingProjectSinkEnricher_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newLoggingProjectSinkEnricher().(*loggingProjectSinkEnricher)
	_, err := e.EnrichByID(context.Background(), nil, EnrichClients{Logging: &loggingv2.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil identity")
}
