package gcpdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
)

func TestIdentityPlatformConfigListNonCAI_ReturnsConfigWhenActivated(t *testing.T) {
	t.Parallel()
	fake := &fakeIdentityPlatformConfigLister{
		cfg: &gcpIdentityPlatformConfig{
			Name:                     "projects/real-proj/config",
			AutodeleteAnonymousUsers: true,
			AuthorizedDomains:        []string{"example.com"},
		},
	}
	d := newIdentityPlatformConfigDiscoverer(fake).(*identityPlatformConfigDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", nil, progress.NopEmitter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d configs, want 1 (singleton)", len(got))
	}
	if got[0].Identity.Type != "google_identity_platform_config" {
		t.Errorf("Type=%q", got[0].Identity.Type)
	}
	// Import ID is just the project — the singleton has no other
	// disambiguator.
	if got[0].Identity.ImportID != "real-proj" {
		t.Errorf("ImportID=%q, want real-proj", got[0].Identity.ImportID)
	}
	if got[0].Identity.NativeIDs["service"] != identityPlatformConfigService {
		t.Errorf("NativeIDs[service]=%q, want %q", got[0].Identity.NativeIDs["service"], identityPlatformConfigService)
	}
}

func TestIdentityPlatformConfigListNonCAI_NotActivatedYieldsZero(t *testing.T) {
	t.Parallel()
	// cfg=nil + err=nil is the "Identity Platform not activated"
	// state the lister returns when getConfig hits 404.
	fake := &fakeIdentityPlatformConfigLister{cfg: nil, err: nil}
	d := newIdentityPlatformConfigDiscoverer(fake).(*identityPlatformConfigDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", nil, progress.NopEmitter{})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("got=%v, want nil (project hasn't activated Identity Platform)", got)
	}
}

func TestIdentityPlatformConfigListNonCAI_ErrorPropagates(t *testing.T) {
	t.Parallel()
	want := errors.New("API not enabled")
	fake := &fakeIdentityPlatformConfigLister{err: want}
	d := newIdentityPlatformConfigDiscoverer(fake).(*identityPlatformConfigDiscoverer)
	_, err := d.ListNonCAI(context.Background(), "real-proj", "", nil, progress.NopEmitter{})
	if !errors.Is(err, want) {
		t.Errorf("err=%v, want wrapping %v", err, want)
	}
}

func TestIdentityPlatformConfigListNonCAI_NilListerTolerated(t *testing.T) {
	t.Parallel()
	d := newIdentityPlatformConfigDiscoverer(nil).(*identityPlatformConfigDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", nil, progress.NopEmitter{})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("got=%v, want nil", got)
	}
}

func TestIsIdentityPlatformNotActivated(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "404 message", err: errors.New("googleapi: Error 404: Config not found"), want: true},
		{name: "NOT_FOUND marker", err: errors.New("rpc error: code = NotFound desc = NOT_FOUND"), want: true},
		{name: "permission denied", err: errors.New("permission denied"), want: false},
		{name: "generic 500", err: errors.New("googleapi: Error 500: backend error"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isIdentityPlatformNotActivated(tc.err); got != tc.want {
				t.Errorf("isIdentityPlatformNotActivated(%v)=%v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
