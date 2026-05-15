package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/api/googleapi"
	iamv1 "google.golang.org/api/iam/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// serviceAccountEnricher implements AttributeEnricher AND ByIDEnricher
// for google_service_account. Pairs with serviceAccountDiscoverer.
//
// Hand-rolled (no .gen.go partner) because the SA API surface is
// trivial — 8 scalar fields, no nested blocks. The cost of an enrichgen
// target exceeds the cost of maintaining this directly.
//
// IAM API quirk: ServiceAccounts.Get takes the fully-qualified
// "projects/<p>/serviceAccounts/<email>" name as a single argument.
// The enricher pulls the email from Identity (NameHint or NativeIDs).
//
// account_id is the local part of the SA email (before the '@'). TF
// treats it as a Required, ForceNew field; we derive it from the
// returned Email since the API doesn't expose it as a separate field.
type serviceAccountEnricher struct {
	fetch func(ctx context.Context, svc *iamv1.Service, name string) (*iamv1.ServiceAccount, error)
}

func newServiceAccountEnricher() AttributeEnricher {
	return &serviceAccountEnricher{fetch: defaultServiceAccountFetch}
}

var (
	_ AttributeEnricher = (*serviceAccountEnricher)(nil)
	_ ByIDEnricher      = (*serviceAccountEnricher)(nil)
)

func (serviceAccountEnricher) ResourceType() string { return serviceAccountTFType }

func (e serviceAccountEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchAndMap(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

func (e serviceAccountEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("service_account: nil identity")
	}
	return e.fetchAndMap(ctx, identity, c)
}

func (e serviceAccountEnricher) fetchAndMap(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.IAM == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if c.ProjectID == "" {
		return nil, fmt.Errorf("service_account: EnrichClients.ProjectID required")
	}
	email := serviceAccountEmailForEnrich(id)
	if email == "" {
		return nil, fmt.Errorf("service_account: cannot derive email from Identity (Address=%q ImportID=%q NameHint=%q NativeIDs.email=%q NativeIDs.asset_name=%q)",
			id.Address, id.ImportID, id.NameHint, id.NativeIDs["email"], id.NativeIDs["asset_name"])
	}
	name := fmt.Sprintf("projects/%s/serviceAccounts/%s", c.ProjectID, email)
	sa, err := e.fetch(ctx, c.IAM, name)
	if err != nil {
		if isIAMNotFound(err) {
			return nil, fmt.Errorf("service_account: %s: %w", name, ErrNotFound)
		}
		return nil, fmt.Errorf("service_account: get %s: %w", name, err)
	}
	typed := mapServiceAccount(sa, c.ProjectID)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("service_account: marshal Attrs: %w", err)
	}
	return raw, nil
}

// serviceAccountEmailForEnrich resolves the SA email address from the
// Identity. Precedence: NativeIDs["email"] (canonical, set by the
// Discoverer), NameHint, ImportID parse, NativeIDs["asset_name"] parse.
func serviceAccountEmailForEnrich(id *imported.ResourceIdentity) string {
	if id == nil {
		return ""
	}
	if e := id.NativeIDs["email"]; e != "" {
		return e
	}
	if id.NameHint != "" {
		return id.NameHint
	}
	if id.ImportID != "" {
		if e, err := serviceAccountEmailFromID(id.ImportID); err == nil {
			return e
		}
	}
	if asset := id.NativeIDs["asset_name"]; asset != "" {
		if e, err := serviceAccountEmailFromID(asset); err == nil {
			return e
		}
	}
	return ""
}

func defaultServiceAccountFetch(ctx context.Context, svc *iamv1.Service, name string) (*iamv1.ServiceAccount, error) {
	return svc.Projects.ServiceAccounts.Get(name).Context(ctx).Do()
}

// isIAMNotFound mirrors isComputeNotFound: 404 from the IAM REST API
// is the not-found signal.
func isIAMNotFound(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusNotFound
	}
	return false
}

// mapServiceAccount converts an *iamv1.ServiceAccount into the typed
// Layer-1 *generated.GoogleServiceAccount model.
//
// account_id is derived from the email's local part because the IAM
// API doesn't expose it as a discrete field; the email is canonical
// (account_id@<gcp-project>.iam.gserviceaccount.com).
//
// Computed-only TF fields skipped per decision #5: id, member, name
// (TF Computed when the resource is read back; the provider populates
// from email after import). create_ignore_already_exists is a TF-only
// sentinel with no API analogue — emit as default false.
func mapServiceAccount(b *iamv1.ServiceAccount, projectID string) *generated.GoogleServiceAccount {
	out := &generated.GoogleServiceAccount{}

	if b.Email != "" {
		out.Email = generated.LiteralOf(b.Email)
		// account_id = local part of the email.
		if i := strings.IndexByte(b.Email, '@'); i > 0 {
			out.AccountID = generated.LiteralOf(b.Email[:i])
		}
	}
	if projectID != "" {
		out.Project = generated.LiteralOf(projectID)
	}
	if b.DisplayName != "" {
		out.DisplayName = generated.LiteralOf(b.DisplayName)
	}
	if b.Description != "" {
		out.Description = generated.LiteralOf(b.Description)
	}
	if b.Disabled {
		out.Disabled = generated.LiteralOf(b.Disabled)
	}
	if b.UniqueId != "" {
		out.UniqueID = generated.LiteralOf(b.UniqueId)
	}

	// create_ignore_already_exists is a TF-only sentinel; default false
	// matches the schema default (decision-#34 stable round-trip).
	out.CreateIgnoreAlreadyExists = generated.LiteralOf(false)

	return out
}

