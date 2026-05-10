// Live smoke driver for prototype/vpcquery (#339).
// Run with AWS creds for CUST3 (031780745048):
//
//	go run ./cmd/insideout-import/awsdiscover/prototype/vpcquery/cmd/smoke \
//	  -project=io-oukrhfwhmflf -region=us-east-1
//
// Compares prototype output to the hand-written vpcDiscoverer for parity.
// Requires terraform 1.14+ on PATH (see docs/terraform-query-prototype.md).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sort"

	"github.com/aws/aws-sdk-go-v2/config"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover/prototype/vpcquery"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

func main() {
	project := flag.String("project", "", "Project tag value (empty = list all)")
	region := flag.String("region", "us-east-1", "AWS region")
	account := flag.String("account", "031780745048", "AWS account ID (for Identity stamping)")
	flag.Parse()

	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(*region))
	if err != nil {
		fmt.Fprintln(os.Stderr, "load config:", err)
		os.Exit(2)
	}

	args := awsdiscover.DiscoverArgs{
		Project:   *project,
		Regions:   []string{*region},
		AccountID: *account,
	}

	prod := awsdiscover.NewAWSDiscoverer(cfg)
	prodOut, err := prod.DiscoverTypes(ctx, []string{"aws_vpc"}, args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "production discover:", err)
		os.Exit(2)
	}

	proto := vpcquery.NewDiscoverer(cfg)
	protoOut, err := proto.Discover(ctx, args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "prototype discover:", err)
		os.Exit(2)
	}

	prodIDs := importIDs(prodOut)
	protoIDs := importIDs(protoOut)
	sort.Strings(prodIDs)
	sort.Strings(protoIDs)

	fmt.Printf("PRODUCTION: %d VPCs  %v\n", len(prodIDs), prodIDs)
	fmt.Printf("PROTOTYPE:  %d VPCs  %v\n", len(protoIDs), protoIDs)

	if !reflect.DeepEqual(prodIDs, protoIDs) {
		fmt.Fprintln(os.Stderr, "MISMATCH")
		os.Exit(1)
	}

	// Spot-check Identity equality on the first match.
	if len(prodOut) > 0 && len(protoOut) > 0 {
		p := prodOut[0].Identity
		q := protoOut[0].Identity
		fmt.Printf("PROD[0] Address=%s NameHint=%s Tags=%v\n", p.Address, p.NameHint, p.Tags)
		fmt.Printf("PROT[0] Address=%s NameHint=%s Tags=%v\n", q.Address, q.NameHint, q.Tags)
		if p.Address != q.Address {
			fmt.Fprintf(os.Stderr, "address mismatch: prod=%s proto=%s\n", p.Address, q.Address)
			os.Exit(1)
		}
		if !reflect.DeepEqual(p.Tags, q.Tags) {
			fmt.Fprintf(os.Stderr, "tags mismatch: prod=%v proto=%v\n", p.Tags, q.Tags)
			os.Exit(1)
		}
	}
	fmt.Println("PARITY OK")
}

func importIDs(rs []imported.ImportedResource) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Identity.ImportID)
	}
	return out
}
