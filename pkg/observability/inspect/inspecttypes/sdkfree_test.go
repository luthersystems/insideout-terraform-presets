package inspecttypes_test

import (
	"os/exec"
	"strings"
	"testing"
)

// forbiddenSDKImportPrefixes — see the metricstypes twin. The inspect
// wire types must stay SDK-free so reliable's thin proxy (reliable#2153)
// can deserialize a BatchResponse without dragging in the AWS / GCP SDK
// clients the parent inspect.Dispatcher pulls in.
var forbiddenSDKImportPrefixes = []string{
	"github.com/aws/aws-sdk-go",
	"cloud.google.com/go",
	"google.golang.org/api",
	"google.golang.org/genproto",
}

// TestInspectTypesSDKFree fails if any cloud SDK leaks into the leaf
// package's transitive dependencies.
func TestInspectTypesSDKFree(t *testing.T) {
	t.Parallel()
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, bad := range forbiddenSDKImportPrefixes {
			if dep == bad || strings.HasPrefix(dep, bad+"/") {
				t.Errorf("inspecttypes must stay SDK-free but transitively imports %q (matched %q)", dep, bad)
			}
		}
	}
}
