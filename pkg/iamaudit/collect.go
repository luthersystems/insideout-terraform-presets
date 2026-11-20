package iamaudit

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// RoleFilter selects the role sessions to audit: a call counts when its
// session was issued by one of ARNs, or by a role whose ARN matches one of
// Patterns. Patterns exist because each project has its own deploy roles
// (<short_project_id>-luther-terraform, …-account-admin-0), and a deploy
// matrix creates many projects on one test account.
type RoleFilter struct {
	ARNs     []string
	Patterns []*regexp.Regexp
}

// Match reports whether roleARN is selected. An empty filter selects nothing,
// so a run with no role given audits nothing rather than everything.
func (f RoleFilter) Match(roleARN string) bool {
	if roleARN == "" {
		return false
	}
	for _, a := range f.ARNs {
		if a == roleARN {
			return true
		}
	}
	for _, re := range f.Patterns {
		if re.MatchString(roleARN) {
			return true
		}
	}
	return false
}

// Empty reports whether the filter selects nothing.
func (f RoleFilter) Empty() bool { return len(f.ARNs) == 0 && len(f.Patterns) == 0 }

// Usage is what the audited roles did with one IAM action.
type Usage struct {
	Action string `json:"action"`
	// Calls counts the events the action authorized.
	Calls int `json:"calls"`
	// ErrorCodes counts the calls that failed, by CloudTrail errorCode. An
	// access-denied error here means the role tried the action and was
	// refused (by the boundary, an SCP or a resource policy).
	ErrorCodes map[string]int `json:"error_codes,omitempty"`
	// ViaService counts the calls an AWS service made with the role's
	// credentials (forward access sessions: EKS, EBS, CloudFormation, …).
	// IAM authorizes those against the role's policy too.
	ViaService int `json:"via_service,omitempty"`
	// Approximate is true when the event did not name the action exactly
	// (see Action.Approximate).
	Approximate bool `json:"approximate,omitempty"`
	// EventNames lists the event source and name that mapped here when
	// they differ from the action, so a reviewer can check the mapping.
	EventNames []string  `json:"event_names,omitempty"`
	Roles      []string  `json:"roles"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
}

// Collector aggregates CloudTrail events into Usage per IAM action. It is
// safe for concurrent use, so sources may feed it from several goroutines.
type Collector struct {
	Roles RoleFilter
	// Start and End bound eventTime, half open: Start <= t < End. A zero
	// value leaves that side open.
	Start, End time.Time

	mu      sync.Mutex
	scanned int
	matched int
	usage   map[string]*usageAcc
	roles   map[string]bool
	regions map[string]bool
}

type usageAcc struct {
	Usage
	roles  map[string]bool
	events map[string]bool
}

// Add counts one event if it is an API call by a selected role inside the
// window.
func (c *Collector) Add(ev Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.scanned++
	if !ev.IsAPICall() {
		return
	}
	role := ev.RoleARN()
	if !c.Roles.Match(role) {
		return
	}
	if !c.Start.IsZero() && ev.EventTime.Before(c.Start) {
		return
	}
	if !c.End.IsZero() && !ev.EventTime.Before(c.End) {
		return
	}
	acts := MapEvent(ev)
	if len(acts) == 0 {
		return
	}
	c.matched++
	if c.usage == nil {
		c.usage = map[string]*usageAcc{}
		c.roles = map[string]bool{}
		c.regions = map[string]bool{}
	}
	c.roles[role] = true
	if ev.AWSRegion != "" {
		c.regions[ev.AWSRegion] = true
	}
	src := strings.TrimSuffix(ev.EventSource, ".amazonaws.com") + ":" + ev.EventName
	for _, a := range acts {
		// IAM action names are case-insensitive; key on the lower case so
		// one action seen under two spellings is counted once.
		key := strings.ToLower(a.Name)
		u := c.usage[key]
		if u == nil {
			u = &usageAcc{
				Usage:  Usage{Action: a.Name, FirstSeen: ev.EventTime, LastSeen: ev.EventTime},
				roles:  map[string]bool{},
				events: map[string]bool{},
			}
			c.usage[key] = u
		}
		u.Calls++
		if ev.ErrorCode != "" {
			if u.ErrorCodes == nil {
				u.ErrorCodes = map[string]int{}
			}
			u.ErrorCodes[ev.ErrorCode]++
		}
		if ev.UserIdentity.InvokedBy != "" {
			u.ViaService++
		}
		u.Approximate = u.Approximate || a.Approximate
		if !strings.EqualFold(src, a.Name) {
			u.events[src] = true
		}
		u.roles[role] = true
		if ev.EventTime.Before(u.FirstSeen) {
			u.FirstSeen = ev.EventTime
		}
		if ev.EventTime.After(u.LastSeen) {
			u.LastSeen = ev.EventTime
		}
	}
}

// Result is the collected usage.
type Result struct {
	EventsScanned int      `json:"events_scanned"`
	EventsMatched int      `json:"events_matched"`
	Roles         []string `json:"roles"`
	Regions       []string `json:"regions"`
	// Used is sorted by action.
	Used []Usage `json:"used"`
}

// Result returns the usage collected so far.
func (c *Collector) Result() Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := Result{
		EventsScanned: c.scanned,
		EventsMatched: c.matched,
		Roles:         sortedKeys(c.roles),
		Regions:       sortedKeys(c.regions),
	}
	for _, u := range c.usage {
		out := u.Usage
		out.Roles = sortedKeys(u.roles)
		out.EventNames = sortedKeys(u.events)
		if u.ErrorCodes != nil {
			out.ErrorCodes = make(map[string]int, len(u.ErrorCodes))
			for k, v := range u.ErrorCodes {
				out.ErrorCodes[k] = v
			}
		}
		r.Used = append(r.Used, out)
	}
	sort.Slice(r.Used, func(i, j int) bool { return r.Used[i].Action < r.Used[j].Action })
	return r
}

// sortedKeys returns the keys of a set, sorted; never nil, so a JSON report
// shows [] for an empty set.
func sortedKeys(m map[string]bool) []string {
	return append([]string{}, slices.Sorted(maps.Keys(m))...)
}

// CompileRolePatterns compiles role ARN patterns for a RoleFilter.
func CompileRolePatterns(pats []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(pats))
	for _, p := range pats {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("role pattern %q: %w", p, err)
		}
		out = append(out, re)
	}
	return out, nil
}
