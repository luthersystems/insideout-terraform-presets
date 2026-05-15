package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	computev1 "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

var (
	_ AttributeEnricher = (*computeInstanceEnricher)(nil)
	_ ByIDEnricher      = (*computeInstanceEnricher)(nil)
)

func instIdentity() imported.ResourceIdentity {
	return imported.ResourceIdentity{
		Cloud:    "gcp",
		Type:     "google_compute_instance",
		NameHint: "io-foo-vm",
		Address:  "google_compute_instance.io_foo_vm",
		ImportID: "projects/my-project/zones/us-central1-a/instances/io-foo-vm",
		Location: "us-central1-a",
		NativeIDs: map[string]string{
			"asset_name": "//compute.googleapis.com/projects/my-project/zones/us-central1-a/instances/io-foo-vm",
			"self_link":  "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/instances/io-foo-vm",
		},
	}
}

func TestMapComputeInstance_Minimal(t *testing.T) {
	t.Parallel()
	src := &computev1.Instance{
		Name:        "io-foo-vm",
		MachineType: "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/machineTypes/e2-small",
	}
	got := mapComputeInstance(src, "my-project", "us-central1-a")

	require.NotNil(t, got.Name)
	assert.Equal(t, "io-foo-vm", *got.Name.Literal)
	require.NotNil(t, got.Project)
	assert.Equal(t, "my-project", *got.Project.Literal)
	require.NotNil(t, got.Zone)
	assert.Equal(t, "us-central1-a", *got.Zone.Literal)
	require.NotNil(t, got.MachineType)
	assert.Equal(t, "e2-small", *got.MachineType.Literal, "machine_type flattened from self-link")
	assert.Empty(t, got.BootDisk)
	assert.Empty(t, got.NetworkInterface)
}

func TestMapComputeInstance_WithBootDiskAndNIC(t *testing.T) {
	t.Parallel()
	src := &computev1.Instance{
		Name:         "io-foo-vm",
		MachineType:  "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/machineTypes/n2-standard-4",
		Description:  "test vm",
		CanIpForward: true,
		Disks: []*computev1.AttachedDisk{
			{
				Boot:       true,
				DeviceName: "persistent-disk-0",
				AutoDelete: true,
				Source:     "projects/my-project/zones/us-central1-a/disks/io-foo-vm",
				DiskEncryptionKey: &computev1.CustomerEncryptionKey{
					KmsKeyName: "projects/my-project/locations/us-central1/keyRings/io-foo-ring/cryptoKeys/io-foo-key",
				},
				InitializeParams: &computev1.AttachedDiskInitializeParams{
					SourceImage: "projects/debian-cloud/global/images/family/debian-12",
					DiskSizeGb:  20,
					DiskType:    "projects/my-project/zones/us-central1-a/diskTypes/pd-ssd",
				},
			},
		},
		NetworkInterfaces: []*computev1.NetworkInterface{
			{
				Network:    "projects/my-project/global/networks/default",
				Subnetwork: "projects/my-project/regions/us-central1/subnetworks/default",
				NetworkIP:  "10.0.0.10",
				NicType:    "GVNIC",
				AccessConfigs: []*computev1.AccessConfig{
					{NatIP: "203.0.113.10", NetworkTier: "PREMIUM"},
				},
			},
		},
		ServiceAccounts: []*computev1.ServiceAccount{
			{Email: "io-foo-sa@my-project.iam.gserviceaccount.com", Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"}},
		},
		Scheduling: &computev1.Scheduling{
			Preemptible:       false,
			OnHostMaintenance: "MIGRATE",
			ProvisioningModel: "STANDARD",
		},
		ShieldedInstanceConfig: &computev1.ShieldedInstanceConfig{
			EnableSecureBoot:          true,
			EnableVtpm:                true,
			EnableIntegrityMonitoring: true,
		},
		Tags: &computev1.Tags{Items: []string{"web", "ssh"}},
		Metadata: &computev1.Metadata{
			Items: []*computev1.MetadataItems{
				{Key: "ssh-keys", Value: stringPtr("alice:ssh-rsa AAAA")},
				{Key: "startup-script", Value: stringPtr("#!/bin/bash\necho hi")},
			},
		},
		Labels: map[string]string{"env": "prod", "goog-managed-by": "ignored"},
	}
	got := mapComputeInstance(src, "my-project", "us-central1-a")

	// Top-level checks.
	require.NotNil(t, got.MachineType)
	assert.Equal(t, "n2-standard-4", *got.MachineType.Literal)
	require.NotNil(t, got.CanIpForward)
	assert.True(t, *got.CanIpForward.Literal)

	// Labels filtered.
	require.NotNil(t, got.Labels)
	assert.Contains(t, got.Labels, "env")
	assert.NotContains(t, got.Labels, "goog-managed-by")

	// Boot disk.
	require.Len(t, got.BootDisk, 1)
	bd := got.BootDisk[0]
	require.NotNil(t, bd.AutoDelete)
	require.NotNil(t, bd.Source)
	require.NotNil(t, bd.KMSKeySelfLink)
	require.Len(t, bd.InitializeParams, 1)
	require.NotNil(t, bd.InitializeParams[0].Image)
	require.NotNil(t, bd.InitializeParams[0].Size)
	require.NotNil(t, bd.InitializeParams[0].Type_)
	assert.Equal(t, "pd-ssd", *bd.InitializeParams[0].Type_.Literal, "disk type flattened from self-link")

	// NIC.
	require.Len(t, got.NetworkInterface, 1)
	require.Len(t, got.NetworkInterface[0].AccessConfig, 1)

	// SA.
	require.Len(t, got.ServiceAccount, 1)
	require.NotNil(t, got.ServiceAccount[0].Email)
	require.Len(t, got.ServiceAccount[0].Scopes, 1)

	// Scheduling.
	require.Len(t, got.Scheduling, 1)
	require.NotNil(t, got.Scheduling[0].OnHostMaintenance)

	// Shielded.
	require.Len(t, got.ShieldedInstanceConfig, 1)

	// Tags + metadata.
	require.Len(t, got.Tags, 2)
	require.NotNil(t, got.Metadata)
	assert.Contains(t, got.Metadata, "ssh-keys")
	assert.NotContains(t, got.Metadata, "startup-script", "startup-script lives in metadata_startup_script")
	require.NotNil(t, got.MetadataStartupScript)
}

func TestComputeInstanceEnrich_ClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newComputeInstanceEnricher()
	ir := &imported.ImportedResource{Identity: instIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{Compute: nil})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestComputeInstanceEnrich_NameMissing(t *testing.T) {
	t.Parallel()
	e := computeInstanceEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, _, _, _ string) (*computev1.Instance, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{Type: "google_compute_instance"}}
	err := e.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive zone/name")
}

func TestComputeInstanceEnrich_NotFound(t *testing.T) {
	t.Parallel()
	e := computeInstanceEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, _, _, _ string) (*computev1.Instance, error) {
			return nil, &googleapi.Error{Code: http.StatusNotFound}
		},
	}
	ir := &imported.ImportedResource{Identity: instIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestComputeInstanceEnrich_FetchError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("500 internal")
	e := computeInstanceEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, _, _, _ string) (*computev1.Instance, error) {
			return nil, wantErr
		},
	}
	ir := &imported.ImportedResource{Identity: instIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestComputeInstanceEnrich_PopulatesAttrs(t *testing.T) {
	t.Parallel()
	inst := &computev1.Instance{
		Name:        "io-foo-vm",
		MachineType: "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/machineTypes/e2-small",
	}
	var gotProj, gotZone, gotName string
	e := computeInstanceEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, p, z, n string) (*computev1.Instance, error) {
			gotProj, gotZone, gotName = p, z, n
			return inst, nil
		},
	}
	ir := &imported.ImportedResource{Identity: instIdentity()}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"}))
	assert.Equal(t, "my-project", gotProj)
	assert.Equal(t, "us-central1-a", gotZone)
	assert.Equal(t, "io-foo-vm", gotName)

	decoded, err := generated.UnmarshalAttrs("google_compute_instance", ir.Attrs)
	require.NoError(t, err)
	g, ok := decoded.(*generated.GoogleComputeInstance)
	require.True(t, ok)
	require.NotNil(t, g.Name)
	assert.Equal(t, "io-foo-vm", *g.Name.Literal)
}

func TestComputeInstanceEnrichByID(t *testing.T) {
	t.Parallel()
	e := computeInstanceEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, _, _, _ string) (*computev1.Instance, error) {
			return &computev1.Instance{Name: "io-foo-vm"}, nil
		},
	}
	id := instIdentity()
	raw, err := e.EnrichByID(context.Background(), &id, EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"})
	require.NoError(t, err)
	require.NotEmpty(t, raw)
	var p map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &p))
}

func TestComputeInstanceEnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newComputeInstanceEnricher().(*computeInstanceEnricher)
	_, err := e.EnrichByID(context.Background(), nil, EnrichClients{Compute: &computev1.Service{}, ProjectID: "p"})
	require.Error(t, err)
}

func TestComputeInstanceRegisteredOnAggregator(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})
	enr, ok := g.byTypeEnricher["google_compute_instance"]
	require.True(t, ok)
	require.NotNil(t, enr)
	assert.Equal(t, "google_compute_instance", enr.ResourceType())
}

func stringPtr(s string) *string { return &s }
