// EC2 Instance Connect URL enrichment.
//
// Ported from the InsideOut backend internal/agentapi/aws_inspect.go:1463
// (enrichEC2WithConnectURLs). The InsideOut backend's aws_connect_info.go file is
// session/Oracle webserver glue and is NOT ported here — only the URL
// builder for the inspector response is needed.
//
// The console URL opens the in-browser SSH terminal AWS exposes via EC2
// Instance Connect. URLs are only attached to instances reporting
// State.Name == "running" so the UI doesn't surface a non-functional
// link for stopped/terminating boxes.

package aws

import (
	"encoding/json"
	"fmt"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// enrichEC2WithConnectURLs converts the raw AWS Reservations response
// into a JSON-friendly []map[string]any with InstanceConnectURL fields
// added to every running instance. Falls back to the input on JSON
// failure rather than dropping the response — the inspector's
// describe-instances contract is "always return SOMETHING about the
// instance even if the URL enrichment fails".
func enrichEC2WithConnectURLs(region string, reservations []ec2types.Reservation) any {
	raw, err := json.Marshal(reservations)
	if err != nil {
		return reservations
	}
	var enriched []map[string]any
	if err := json.Unmarshal(raw, &enriched); err != nil {
		return reservations
	}
	for _, res := range enriched {
		instances, ok := res["Instances"].([]any)
		if !ok {
			continue
		}
		for _, inst := range instances {
			m, ok := inst.(map[string]any)
			if !ok {
				continue
			}
			instanceID, _ := m["InstanceId"].(string)
			if instanceID == "" {
				continue
			}
			if state, ok := m["State"].(map[string]any); ok {
				if name, _ := state["Name"].(string); name != "running" {
					continue
				}
			}
			m["InstanceConnectURL"] = fmt.Sprintf(
				"https://%s.console.aws.amazon.com/ec2-instance-connect/ssh?region=%s&connType=standard&instanceId=%s&osUser=ubuntu&sshPort=22&addressFamily=ipv4",
				region, region, instanceID,
			)
		}
	}
	return enriched
}
