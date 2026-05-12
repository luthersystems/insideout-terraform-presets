package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestLoggingProjectSinkListNonCAI_FiltersBuiltinsAndProject(t *testing.T) {
	t.Parallel()
	fake := &fakeLoggingSinkLister{
		sinks: []gcpLoggingSink{
			{Name: "_Default", FullName: "projects/real-proj/sinks/_Default", Destination: "logging.googleapis.com/projects/real-proj/locations/global/buckets/_Default"},
			{Name: "_Required", FullName: "projects/real-proj/sinks/_Required", Destination: "logging.googleapis.com/projects/real-proj/locations/global/buckets/_Required"},
			{Name: "io-foo-audit-sink", FullName: "projects/real-proj/sinks/io-foo-audit-sink", Destination: "storage.googleapis.com/io-foo-audit-bucket"},
			{Name: "other-stack-sink", FullName: "projects/real-proj/sinks/other-stack-sink", Destination: "storage.googleapis.com/other"},
		},
	}
	d := newLoggingProjectSinkDiscoverer(fake).(*loggingProjectSinkDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "io-foo", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Builtins (_Default, _Required) filtered + stack-project filter
	// keeps only io-foo-audit-sink.
	if len(got) != 1 {
		t.Fatalf("got %d sinks, want 1 (builtins + non-project filtered): %+v", len(got), got)
	}
	if got[0].Identity.NameHint != "io-foo-audit-sink" {
		t.Errorf("NameHint=%q, want io-foo-audit-sink", got[0].Identity.NameHint)
	}
	if got[0].Identity.Type != "google_logging_project_sink" {
		t.Errorf("Type=%q", got[0].Identity.Type)
	}
	if len(fake.calls) != 1 || fake.calls[0] != "real-proj" {
		t.Errorf("calls=%v, want [real-proj]", fake.calls)
	}
}

// TestLoggingProjectSinkListNonCAI_RejectsLeadingProjectsSegmentSpuriousMatch
// pins the /review fix that swapped strings.Contains for the
// trailing-segment matchesNamePrefix helper. The leading
// "projects/<gcp-project-id>/sinks/..." path segment could otherwise
// match a sink whose own short name doesn't actually carry the stack
// project — if the gcp-project-id happened to contain "io-foo" as a
// substring, the sink would be incorrectly attributed.
func TestLoggingProjectSinkListNonCAI_RejectsLeadingProjectsSegmentSpuriousMatch(t *testing.T) {
	t.Parallel()
	fake := &fakeLoggingSinkLister{
		sinks: []gcpLoggingSink{
			// FullName's leading projects/ segment contains "io-foo"
			// but the trailing sink name does NOT — must be filtered.
			{Name: "audit-sink", FullName: "projects/io-foo-prod/sinks/audit-sink"},
		},
	}
	d := newLoggingProjectSinkDiscoverer(fake).(*loggingProjectSinkDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "io-foo-prod", "io-foo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d sinks, want 0 — leading projects/ segment must not falsely attribute", len(got))
	}
}

func TestLoggingProjectSinkListNonCAI_NilListerTolerated(t *testing.T) {
	t.Parallel()
	d := newLoggingProjectSinkDiscoverer(nil).(*loggingProjectSinkDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "io-foo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("got=%v, want nil (nil-lister fallthrough)", got)
	}
}

func TestLoggingProjectSinkListNonCAI_ListerErrorPropagates(t *testing.T) {
	t.Parallel()
	want := errors.New("logging API down")
	fake := &fakeLoggingSinkLister{err: want}
	d := newLoggingProjectSinkDiscoverer(fake).(*loggingProjectSinkDiscoverer)
	_, err := d.ListNonCAI(context.Background(), "real-proj", "io-foo", nil)
	if !errors.Is(err, want) {
		t.Errorf("err=%v, want wrapping %v", err, want)
	}
}

func TestLoggingProjectSinkListNonCAI_EmptyStackProjectMatchesAll(t *testing.T) {
	t.Parallel()
	fake := &fakeLoggingSinkLister{
		sinks: []gcpLoggingSink{
			{Name: "io-foo-sink", FullName: "projects/p/sinks/io-foo-sink"},
			{Name: "another-sink", FullName: "projects/p/sinks/another-sink"},
		},
	}
	d := newLoggingProjectSinkDiscoverer(fake).(*loggingProjectSinkDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("got %d sinks, want 2 (empty stack-project disables filter)", len(got))
	}
}

func TestIsBuiltinLoggingSink(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want bool
	}{
		{name: "_Default", want: true},
		{name: "_Required", want: true},
		{name: "io-foo-sink", want: false},
		{name: "default", want: false}, // case-sensitive
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isBuiltinLoggingSink(tc.name); got != tc.want {
				t.Errorf("isBuiltinLoggingSink(%q)=%v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
