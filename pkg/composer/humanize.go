package composer

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var booleanFields = map[string]bool{
	"ha":                      true,
	"haControlPlane":          true,
	"versioning":              true,
	"mfaRequired":             true,
	"multiAz":                 true,
	"regional":                true,
	"highAvailability":        true,
	"enableInstanceConnect":   true,
	"enableContainerInsights": true,
	"enableServiceConnect":    true,
	"selfSignupAllowed":       true,
}

var durationFields = map[string]bool{
	"defaultTtl":               true,
	"visibilityTimeout":        true,
	"retentionPeriod":          true,
	"messageRetentionDuration": true,
	"timeout":                  true,
}

var storageSizeFields = map[string]bool{
	"storageSize":       true,
	"diskSizePerServer": true,
	"diskSizeGb":        true,
	"memorySizeGb":      true,
}

var enumMaps = map[string]map[string]string{
	"type": {
		"provisioned":     "Provisioned",
		"pay_per_request": "Pay Per Request",
		"on_demand":       "On-Demand",
		"standard":        "Standard",
		"fifo":            "FIFO",
	},
	"deploymentType": {
		"single_node": "Single Node",
		"multi_node":  "Multi-Node",
	},
	"controlPlaneVisibility": {
		"public":  "Public",
		"private": "Private",
	},
	"storageClass": {
		"STANDARD": "Standard",
		"NEARLINE": "Nearline",
		"COLDLINE": "Coldline",
		"ARCHIVE":  "Archive",
	},
}

var durationRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*(s|sec|seconds?|m|min|minutes?|h|hr|hours?|d|days?)$`)

func formatDurationUnit(n float64, unit string) string {
	u := strings.ToLower(unit)
	plural := n != 1
	switch {
	case strings.HasPrefix(u, "s"):
		if plural {
			return fmt.Sprintf("%g seconds", n)
		}
		return fmt.Sprintf("%g second", n)
	case strings.HasPrefix(u, "m"):
		if plural {
			return fmt.Sprintf("%g minutes", n)
		}
		return fmt.Sprintf("%g minute", n)
	case strings.HasPrefix(u, "h"):
		if plural {
			return fmt.Sprintf("%g hours", n)
		}
		return fmt.Sprintf("%g hour", n)
	case strings.HasPrefix(u, "d"):
		if plural {
			return fmt.Sprintf("%g days", n)
		}
		return fmt.Sprintf("%g day", n)
	}
	return fmt.Sprintf("%g %s", n, unit)
}

func formatSeconds(s int) string {
	switch {
	case s < 60:
		if s == 1 {
			return "1 second"
		}
		return fmt.Sprintf("%d seconds", s)
	case s < 3600:
		m := (s + 30) / 60
		if m == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", m)
	case s < 86400:
		h := (s + 1800) / 3600
		if h == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", h)
	default:
		d := (s + 43200) / 86400
		if d == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", d)
	}
}

func humanizeDuration(raw string) string {
	if m := durationRe.FindStringSubmatch(raw); m != nil {
		n, _ := strconv.ParseFloat(m[1], 64)
		return formatDurationUnit(n, m[2])
	}
	if s, err := strconv.Atoi(raw); err == nil {
		return formatSeconds(s)
	}
	return raw
}

// HumanizeFieldValue converts a raw config field value to a human-readable string.
func HumanizeFieldValue(field, value string) string {
	if value == "" {
		return value
	}

	if booleanFields[field] {
		switch strings.ToLower(value) {
		case "true":
			return "Yes"
		case "false":
			return "No"
		}
		return value
	}

	if durationFields[field] {
		return humanizeDuration(value)
	}

	if field == "retentionDays" {
		if _, err := strconv.Atoi(value); err == nil {
			return value + " days"
		}
		return value
	}

	if em, ok := enumMaps[field]; ok {
		if label, ok := em[value]; ok {
			return label
		}
		return value
	}

	if storageSizeFields[field] {
		if _, err := strconv.Atoi(value); err == nil {
			return value + " GB"
		}
	}

	return value
}
