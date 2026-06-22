package metricstypes_test

import (
	"os/exec"
	"strings"
	"testing"
)

// forbiddenSDKImportPrefixes are the heavy cloud-SDK import roots that
// must never appear in this leaf package's transitive dependency set.
// The whole point of metricstypes (reliable#2153) is that a proxy
// consumer can deserialize a MetricsResult without pulling the
// CloudWatch / Cloud Monitoring clients into its binary; a stray import
// here silently reintroduces that weight at the consumer. Cite the
// triggering example in the comment so the next author understands the
// blast radius: reliable's cmd/api Vercel function is at the 250 MB hard
// limit and these SDKs are the bulk of it.
var forbiddenSDKImportPrefixes = []string{
	"github.com/aws/aws-sdk-go",    // SDK v1
	"github.com/aws/aws-sdk-go-v2", // SDK v2 — this repo's only AWS SDK; the "-v2" suffix is NOT matched by the v1 prefix (a bare HasPrefix(dep, "aws-sdk-go"+"/") never fires on "aws-sdk-go-v2/...").
	"cloud.google.com/go",          // GCP client libraries
	"google.golang.org/api",        // GCP discovery / REST clients
	"google.golang.org/genproto",   // dragged in by the GCP clients
}

// TestMetricsTypesSDKFree fails if any cloud SDK leaks into the leaf
// package's transitive dependencies. Runs `go list -deps` on the
// package under test — go is always present in CI.
func TestMetricsTypesSDKFree(t *testing.T) {
	t.Parallel()
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, bad := range forbiddenSDKImportPrefixes {
			if dep == bad || strings.HasPrefix(dep, bad+"/") {
				t.Errorf("metricstypes must stay SDK-free but transitively imports %q (matched %q)", dep, bad)
			}
		}
	}
}
