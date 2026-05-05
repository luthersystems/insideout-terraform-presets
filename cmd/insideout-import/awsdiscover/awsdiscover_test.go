package awsdiscover

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

type fakeDiscoverer struct {
	t       string
	out     []imported.ImportedResource
	err     error
	called  int
	gotProj string
	gotReg  string
	gotAcct string
}

func (f *fakeDiscoverer) ResourceType() string { return f.t }
func (f *fakeDiscoverer) Discover(_ context.Context, project, region, accountID string) ([]imported.ImportedResource, error) {
	f.called++
	f.gotProj, f.gotReg, f.gotAcct = project, region, accountID
	return f.out, f.err
}

func ir(addr string) imported.ImportedResource {
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: addr, ImportID: addr},
		Tier:     imported.TierImportedFlat,
		Source:   imported.SourceImporter,
	}
}

func TestDiscoverTypes_DefaultsToAllSupported(t *testing.T) {
	t.Parallel()
	a := &fakeDiscoverer{t: "type_a", out: []imported.ImportedResource{ir("a1"), ir("a2")}}
	b := &fakeDiscoverer{t: "type_b", out: []imported.ImportedResource{ir("b1")}}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_a": a, "type_b": b}}

	got, err := agg.DiscoverTypes(context.Background(), nil, "p", "r", "acc")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("len=%d, want 3", len(got))
	}
	if a.called != 1 || b.called != 1 {
		t.Errorf("each discoverer called once; got a=%d b=%d", a.called, b.called)
	}
	if a.gotProj != "p" || a.gotReg != "r" || a.gotAcct != "acc" {
		t.Errorf("project/region/accountID not threaded; got %q/%q/%q", a.gotProj, a.gotReg, a.gotAcct)
	}
}

func TestDiscoverTypes_SelectiveOnlyCallsRequested(t *testing.T) {
	t.Parallel()
	a := &fakeDiscoverer{t: "type_a"}
	b := &fakeDiscoverer{t: "type_b"}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_a": a, "type_b": b}}

	if _, err := agg.DiscoverTypes(context.Background(), []string{"type_b"}, "p", "r", "acc"); err != nil {
		t.Fatal(err)
	}
	if a.called != 0 {
		t.Errorf("type_a should not have been called; called=%d", a.called)
	}
	if b.called != 1 {
		t.Errorf("type_b should have been called once; called=%d", b.called)
	}
}

func TestDiscoverTypes_UnknownTypeAggregatesAllErrorsBeforeRunning(t *testing.T) {
	t.Parallel()
	a := &fakeDiscoverer{t: "type_a"}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_a": a}}

	_, err := agg.DiscoverTypes(context.Background(), []string{"type_a", "bogus", "also_bogus"}, "p", "r", "acc")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "bogus") || !strings.Contains(err.Error(), "also_bogus") {
		t.Errorf("error should list every unknown type; got: %v", err)
	}
	if a.called != 0 {
		t.Errorf("no discoverer should run when any type is unknown; type_a called=%d", a.called)
	}
}

func TestDiscoverTypes_PropagatesPerDiscovererError(t *testing.T) {
	t.Parallel()
	a := &fakeDiscoverer{t: "type_a", err: errors.New("Throttling")}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_a": a}}

	_, err := agg.DiscoverTypes(context.Background(), nil, "p", "r", "acc")
	if err == nil || !strings.Contains(err.Error(), "type_a") || !strings.Contains(err.Error(), "Throttling") {
		t.Errorf("expected wrapped error mentioning resource type and underlying cause; got: %v", err)
	}
}

func TestSupportedTypes_IsSorted(t *testing.T) {
	t.Parallel()
	agg := &AWSDiscoverer{byType: map[string]Discoverer{
		"type_z": &fakeDiscoverer{t: "type_z"},
		"type_a": &fakeDiscoverer{t: "type_a"},
		"type_m": &fakeDiscoverer{t: "type_m"},
	}}
	got := agg.SupportedTypes()
	want := []string{"type_a", "type_m", "type_z"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("SupportedTypes()[%d]=%q, want %q (sorted)", i, got[i], w)
		}
	}
}

func TestNewAWSDiscoverer_Registers5PhaseOneTypes(t *testing.T) {
	t.Parallel()
	agg := NewAWSDiscoverer(awsDummyConfig())
	got := agg.SupportedTypes()
	want := map[string]bool{
		"aws_sqs_queue":             false,
		"aws_dynamodb_table":        false,
		"aws_cloudwatch_log_group":  false,
		"aws_secretsmanager_secret": false,
		"aws_lambda_function":       false,
	}
	for _, typ := range got {
		want[typ] = true
	}
	for k, ok := range want {
		if !ok {
			t.Errorf("expected %q to be registered", k)
		}
	}
}
