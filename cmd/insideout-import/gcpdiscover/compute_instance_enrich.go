package gcpdiscover

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	computev1 "google.golang.org/api/compute/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// computeInstanceEnricher implements AttributeEnricher AND ByIDEnricher
// for google_compute_instance. Pairs with computeInstanceDiscoverer.
//
// Hand-rolled (no .gen.go partner) because the Compute Instance schema
// is one of the largest in the provider (40+ nested blocks); an
// enrichgen target would need an override snippet for almost every
// field. Mirrors compute_firewall_enrich.go's cost/benefit shape — the
// curated Layer-2 policy is what matters for drift, and we map that
// subset directly.
//
// Compute API quirk: Instances.Get takes (project, zone, instance) as
// three positional string parameters. The enricher pulls the zone from
// Identity.Location, the short name from Identity hints, and the
// project from EnrichClients.ProjectID.
//
// Sensitive fields:
//
//	metadata / metadata_startup_script — may carry SSH keys, secrets;
//	the Layer-2 policy marks them SensitivityRedacted. The enricher
//	writes the values; the emit/persist layers redact at write time.
//
// network tags vs labels: google_compute_instance.tags is the GCE
// network-tags list (drives firewall source_tags / target_tags), not
// labels. We map both.
type computeInstanceEnricher struct {
	fetch func(ctx context.Context, svc *computev1.Service, project, zone, instance string) (*computev1.Instance, error)
}

func newComputeInstanceEnricher() AttributeEnricher {
	return &computeInstanceEnricher{fetch: defaultComputeInstanceFetch}
}

var (
	_ AttributeEnricher = (*computeInstanceEnricher)(nil)
	_ ByIDEnricher      = (*computeInstanceEnricher)(nil)
)

func (computeInstanceEnricher) ResourceType() string { return computeInstanceTFType }

func (e computeInstanceEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchAndMap(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

func (e computeInstanceEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("compute_instance: nil identity")
	}
	return e.fetchAndMap(ctx, identity, c)
}

func (e computeInstanceEnricher) fetchAndMap(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.Compute == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if c.ProjectID == "" {
		return nil, fmt.Errorf("compute_instance: EnrichClients.ProjectID required (compute API uses project+zone+name positional args)")
	}
	zone, name := computeInstanceZoneAndNameForEnrich(id)
	if zone == "" || name == "" {
		return nil, fmt.Errorf("compute_instance: cannot derive zone/name from Identity (Address=%q ImportID=%q Location=%q NameHint=%q NativeIDs.asset_name=%q)",
			id.Address, id.ImportID, id.Location, id.NameHint, id.NativeIDs["asset_name"])
	}
	inst, err := e.fetch(ctx, c.Compute, c.ProjectID, zone, name)
	if err != nil {
		if isComputeNotFound(err) {
			return nil, fmt.Errorf("compute_instance: %s/%s/%s: %w", c.ProjectID, zone, name, ErrNotFound)
		}
		return nil, fmt.Errorf("compute_instance: get %s/%s/%s: %w", c.ProjectID, zone, name, err)
	}
	typed := mapComputeInstance(inst, c.ProjectID, zone)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("compute_instance: marshal Attrs: %w", err)
	}
	return raw, nil
}

// computeInstanceZoneAndNameForEnrich resolves (zone, name) from the
// Identity. Precedence: NameHint+Location, ImportID parse,
// NativeIDs["asset_name"] parse.
func computeInstanceZoneAndNameForEnrich(id *imported.ResourceIdentity) (string, string) {
	if id == nil {
		return "", ""
	}
	name := id.NameHint
	zone := id.Location
	if name != "" && zone != "" {
		return zone, name
	}
	if id.ImportID != "" {
		if z, n, err := computeInstancePartsFromID(id.ImportID); err == nil {
			if name == "" {
				name = n
			}
			if zone == "" {
				zone = z
			}
		}
	}
	if (name == "" || zone == "") && id.NativeIDs["asset_name"] != "" {
		if z, n, err := computeInstancePartsFromID(id.NativeIDs["asset_name"]); err == nil {
			if name == "" {
				name = n
			}
			if zone == "" {
				zone = z
			}
		}
	}
	return zone, name
}

func defaultComputeInstanceFetch(ctx context.Context, svc *computev1.Service, project, zone, instance string) (*computev1.Instance, error) {
	return svc.Instances.Get(project, zone, instance).Context(ctx).Do()
}

// mapComputeInstance converts a *computev1.Instance into the typed
// Layer-1 *generated.GoogleComputeInstance model.
//
// Field coverage tracks the curated Layer-2 policy:
//
//	identity: name, project, zone, hostname
//	tuning: machine_type, min_cpu_platform, description, can_ip_forward,
//	        deletion_protection, desired_status, enable_display,
//	        resource_policies, tags, labels, metadata,
//	        metadata_startup_script
//	blocks: boot_disk (source, device_name, auto_delete, initialize_params,
//	        kms_key_self_link), network_interface, service_account,
//	        scheduling, shielded_instance_config
//
// Computed-only TF fields skipped per decision #5: id, self_link,
// cpu_platform, creation_timestamp, current_status, effective_labels,
// label_fingerprint, instance_id, metadata_fingerprint,
// tags_fingerprint, terraform_labels.
//
// machine_type: API returns a self-link
// (https://.../machineTypes/<t>); TF stores the short name. Flatten
// to the trailing segment for stable round-trip.
//
// desired_status: TF-only attribute that maps from the API's Status
// field (the runtime status) when current_status is set; for stable
// round-trip we read but don't translate (it'd produce drift on a
// freshly-imported running instance). Leave for downstream comparator.
func mapComputeInstance(b *computev1.Instance, projectID, zone string) *generated.GoogleComputeInstance {
	out := &generated.GoogleComputeInstance{}

	if b.Name != "" {
		out.Name = generated.LiteralOf(b.Name)
	}
	if projectID != "" {
		out.Project = generated.LiteralOf(projectID)
	}
	if zone != "" {
		out.Zone = generated.LiteralOf(zone)
	}
	if b.MachineType != "" {
		out.MachineType = generated.LiteralOf(shortFromSelfLink(b.MachineType))
	}
	if b.MinCpuPlatform != "" {
		out.MinCPUPlatform = generated.LiteralOf(b.MinCpuPlatform)
	}
	if b.Description != "" {
		out.Description = generated.LiteralOf(b.Description)
	}
	if b.Hostname != "" {
		out.Hostname = generated.LiteralOf(b.Hostname)
	}
	if b.CanIpForward {
		out.CanIpForward = generated.LiteralOf(b.CanIpForward)
	}
	if b.DeletionProtection {
		out.DeletionProtection = generated.LiteralOf(b.DeletionProtection)
	}
	if b.KeyRevocationActionType != "" {
		out.KeyRevocationActionType = generated.LiteralOf(b.KeyRevocationActionType)
	}

	// Labels (with goog-* filter).
	if len(b.Labels) > 0 {
		labels := map[string]*generated.Value[string]{}
		for k, v := range b.Labels {
			if strings.HasPrefix(k, "goog-") || strings.HasPrefix(k, "goog_") {
				continue
			}
			labels[k] = generated.LiteralOf(v)
		}
		if len(labels) > 0 {
			out.Labels = labels
		}
	}

	// Metadata: KV map + special-cased startup-script.
	if b.Metadata != nil && len(b.Metadata.Items) > 0 {
		md := map[string]*generated.Value[string]{}
		for _, it := range b.Metadata.Items {
			if it == nil || it.Key == "" {
				continue
			}
			val := ""
			if it.Value != nil {
				val = *it.Value
			}
			if it.Key == "startup-script" {
				out.MetadataStartupScript = generated.LiteralOf(val)
				continue
			}
			md[it.Key] = generated.LiteralOf(val)
		}
		if len(md) > 0 {
			out.Metadata = md
		}
	}

	// Network tags — the GCE network-tags list, not labels.
	if b.Tags != nil && len(b.Tags.Items) > 0 {
		out.Tags = stringSliceToValues(b.Tags.Items)
	}

	// resource_policies: list of self-links; flatten to short names for
	// stable round-trip with TF state.
	if len(b.ResourcePolicies) > 0 {
		policies := make([]*generated.Value[string], 0, len(b.ResourcePolicies))
		for _, p := range b.ResourcePolicies {
			policies = append(policies, generated.LiteralOf(p))
		}
		out.ResourcePolicies = policies
	}

	// Boot disk.
	if len(b.Disks) > 0 {
		for _, d := range b.Disks {
			if d == nil || !d.Boot {
				continue
			}
			boot := generated.GoogleComputeInstanceBootDisk{}
			if d.DeviceName != "" {
				boot.DeviceName = generated.LiteralOf(d.DeviceName)
			}
			boot.AutoDelete = generated.LiteralOf(d.AutoDelete)
			if d.Source != "" {
				boot.Source = generated.LiteralOf(d.Source)
			}
			if d.Mode != "" {
				boot.Mode = generated.LiteralOf(d.Mode)
			}
			if d.Interface != "" {
				boot.Interface_ = generated.LiteralOf(d.Interface)
			}
			if d.DiskEncryptionKey != nil && d.DiskEncryptionKey.KmsKeyName != "" {
				boot.KMSKeySelfLink = generated.LiteralOf(d.DiskEncryptionKey.KmsKeyName)
			}
			if d.InitializeParams != nil {
				ip := generated.GoogleComputeInstanceBootDiskInitializeParams{}
				if d.InitializeParams.SourceImage != "" {
					ip.Image = generated.LiteralOf(d.InitializeParams.SourceImage)
				}
				if d.InitializeParams.DiskSizeGb != 0 {
					ip.Size = generated.LiteralOf(float64(d.InitializeParams.DiskSizeGb))
				}
				if d.InitializeParams.DiskType != "" {
					ip.Type_ = generated.LiteralOf(shortFromSelfLink(d.InitializeParams.DiskType))
				}
				boot.InitializeParams = []generated.GoogleComputeInstanceBootDiskInitializeParams{ip}
			}
			out.BootDisk = []generated.GoogleComputeInstanceBootDisk{boot}
			break
		}
	}

	// Network interfaces.
	if len(b.NetworkInterfaces) > 0 {
		nics := make([]generated.GoogleComputeInstanceNetworkInterface, 0, len(b.NetworkInterfaces))
		for _, n := range b.NetworkInterfaces {
			if n == nil {
				continue
			}
			nic := generated.GoogleComputeInstanceNetworkInterface{}
			if n.Network != "" {
				nic.Network = generated.LiteralOf(n.Network)
			}
			if n.Subnetwork != "" {
				nic.Subnetwork = generated.LiteralOf(n.Subnetwork)
			}
			if n.NetworkIP != "" {
				nic.NetworkIp = generated.LiteralOf(n.NetworkIP)
			}
			if n.NicType != "" {
				nic.NicType = generated.LiteralOf(n.NicType)
			}
			if n.StackType != "" {
				nic.StackType = generated.LiteralOf(n.StackType)
			}
			if len(n.AccessConfigs) > 0 {
				accs := make([]generated.GoogleComputeInstanceNetworkInterfaceAccessConfig, 0, len(n.AccessConfigs))
				for _, a := range n.AccessConfigs {
					if a == nil {
						continue
					}
					row := generated.GoogleComputeInstanceNetworkInterfaceAccessConfig{}
					if a.NatIP != "" {
						row.NatIp = generated.LiteralOf(a.NatIP)
					}
					if a.NetworkTier != "" {
						row.NetworkTier = generated.LiteralOf(a.NetworkTier)
					}
					if a.PublicPtrDomainName != "" {
						row.PublicPtrDomainName = generated.LiteralOf(a.PublicPtrDomainName)
					}
					accs = append(accs, row)
				}
				if len(accs) > 0 {
					nic.AccessConfig = accs
				}
			}
			nics = append(nics, nic)
		}
		if len(nics) > 0 {
			out.NetworkInterface = nics
		}
	}

	// Service account.
	if len(b.ServiceAccounts) > 0 {
		for _, sa := range b.ServiceAccounts {
			if sa == nil {
				continue
			}
			row := generated.GoogleComputeInstanceServiceAccount{}
			if sa.Email != "" {
				row.Email = generated.LiteralOf(sa.Email)
			}
			if len(sa.Scopes) > 0 {
				row.Scopes = stringSliceToValues(sa.Scopes)
			}
			out.ServiceAccount = []generated.GoogleComputeInstanceServiceAccount{row}
			break
		}
	}

	// Scheduling.
	if b.Scheduling != nil {
		sch := generated.GoogleComputeInstanceScheduling{}
		emit := false
		if b.Scheduling.Preemptible {
			sch.Preemptible = generated.LiteralOf(b.Scheduling.Preemptible)
			emit = true
		}
		// AutomaticRestart is *bool in the SDK as a ForceSendField; map
		// when set explicitly (non-nil pointer would be ideal, but the
		// SDK uses ForceSendFields for these).
		if b.Scheduling.AutomaticRestart != nil {
			sch.AutomaticRestart = generated.LiteralOf(*b.Scheduling.AutomaticRestart)
			emit = true
		}
		if b.Scheduling.OnHostMaintenance != "" {
			sch.OnHostMaintenance = generated.LiteralOf(b.Scheduling.OnHostMaintenance)
			emit = true
		}
		if b.Scheduling.ProvisioningModel != "" {
			sch.ProvisioningModel = generated.LiteralOf(b.Scheduling.ProvisioningModel)
			emit = true
		}
		if emit {
			out.Scheduling = []generated.GoogleComputeInstanceScheduling{sch}
		}
	}

	// Shielded instance config.
	if b.ShieldedInstanceConfig != nil {
		sic := generated.GoogleComputeInstanceShieldedInstanceConfig{
			EnableSecureBoot:          generated.LiteralOf(b.ShieldedInstanceConfig.EnableSecureBoot),
			EnableVtpm:                generated.LiteralOf(b.ShieldedInstanceConfig.EnableVtpm),
			EnableIntegrityMonitoring: generated.LiteralOf(b.ShieldedInstanceConfig.EnableIntegrityMonitoring),
		}
		out.ShieldedInstanceConfig = []generated.GoogleComputeInstanceShieldedInstanceConfig{sic}
	}

	return out
}

// shortFromSelfLink trims a Google self-link
// (https://.../<collection>/<name> or projects/.../<collection>/<name>)
// down to its trailing segment. Mirrors the provider's flatten of
// machine_type, disk_type, etc. — TF state stores the short name, the
// API returns the self-link.
func shortFromSelfLink(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}
