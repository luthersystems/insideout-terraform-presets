package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestAPIGatewayAPIFromAssetAndByID(t *testing.T) {
	t.Parallel()
	d := newAPIGatewayAPIDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//apigateway.googleapis.com/projects/real-proj/locations/global/apis/io-foo-api",
			AssetType: "apigateway.googleapis.com/Api",
			Project:   "real-proj",
			Location:  "global",
			Labels:    map[string]string{"project": "io-foo"},
		},
		"real-proj")
	if got.Identity.Type != "google_api_gateway_api" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "projects/real-proj/locations/global/apis/io-foo-api" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}

	cases := []struct {
		in, wantName string
		wantErr      error
	}{
		{in: "//apigateway.googleapis.com/projects/p/locations/global/apis/api1", wantName: "api1"},
		{in: "projects/p/locations/global/apis/api1", wantName: "api1"},
		{in: "", wantErr: ErrNotSupported},
		{in: "api1", wantErr: ErrNotSupported},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			r, err := d.DiscoverByID(context.Background(), nil, tc.in, "real-proj")
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("err=%v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if r.Identity.NameHint != tc.wantName {
				t.Errorf("NameHint=%q, want %q", r.Identity.NameHint, tc.wantName)
			}
		})
	}
}

func TestAPIGatewayAPIConfigFromAssetAndByID(t *testing.T) {
	t.Parallel()
	d := newAPIGatewayAPIConfigDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//apigateway.googleapis.com/projects/real-proj/locations/global/apis/io-foo-api/configs/io-foo-cfg",
			AssetType: "apigateway.googleapis.com/ApiConfig",
			Project:   "real-proj",
			Location:  "global",
		},
		"real-proj")
	if got.Identity.Type != "google_api_gateway_api_config" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-cfg" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["api"] != "io-foo-api" {
		t.Errorf("NativeIDs[api]=%q, want io-foo-api", got.Identity.NativeIDs["api"])
	}
	if got.Identity.ImportID != "projects/real-proj/locations/global/apis/io-foo-api/configs/io-foo-cfg" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}

	r, err := d.DiscoverByID(context.Background(), nil, "projects/p/locations/global/apis/api1/configs/cfg1", "real-proj")
	if err != nil {
		t.Fatal(err)
	}
	if r.Identity.NameHint != "cfg1" || r.Identity.NativeIDs["api"] != "api1" {
		t.Errorf("got %q/%q, want cfg1/api1", r.Identity.NameHint, r.Identity.NativeIDs["api"])
	}

	_, err = d.DiscoverByID(context.Background(), nil, "", "real-proj")
	if !errors.Is(err, ErrNotSupported) {
		t.Errorf("empty err=%v, want ErrNotSupported", err)
	}
	_, err = d.DiscoverByID(context.Background(), nil, "projects/p/locations/global/apis/api1", "real-proj")
	if !errors.Is(err, ErrNotSupported) {
		t.Errorf("missing configs segment err=%v, want ErrNotSupported", err)
	}
}

func TestAPIGatewayGatewayFromAssetAndByID(t *testing.T) {
	t.Parallel()
	d := newAPIGatewayGatewayDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//apigateway.googleapis.com/projects/real-proj/locations/us-central1/gateways/io-foo-gw",
			AssetType: "apigateway.googleapis.com/Gateway",
			Project:   "real-proj",
			Location:  "us-central1",
		},
		"real-proj")
	if got.Identity.Type != "google_api_gateway_gateway" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "projects/real-proj/locations/us-central1/gateways/io-foo-gw" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	r, err := d.DiscoverByID(context.Background(), nil, "projects/p/locations/us-central1/gateways/g1", "real-proj")
	if err != nil {
		t.Fatal(err)
	}
	if r.Identity.NameHint != "g1" {
		t.Errorf("NameHint=%q, want g1", r.Identity.NameHint)
	}
}
