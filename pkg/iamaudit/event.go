package iamaudit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// Event is the part of a CloudTrail record the audit reads. The same JSON
// shape is in a trail's log files (one element of "Records") and in the
// CloudTrailEvent string of a LookupEvents result.
type Event struct {
	EventTime    time.Time    `json:"eventTime"`
	EventSource  string       `json:"eventSource"`
	EventName    string       `json:"eventName"`
	EventType    string       `json:"eventType"`
	AWSRegion    string       `json:"awsRegion"`
	ErrorCode    string       `json:"errorCode"`
	UserIdentity UserIdentity `json:"userIdentity"`
	// RequestParameters is kept raw: MapEvent reads one field of it (an S3
	// object's versionId), and decoding the rest would cost for nothing.
	RequestParameters json.RawMessage `json:"requestParameters"`
}

// versionID returns requestParameters.versionId, or "" when there is none.
func (e *Event) versionID() string {
	if len(e.RequestParameters) == 0 {
		return ""
	}
	var p struct {
		VersionID string `json:"versionId"`
	}
	if err := json.Unmarshal(e.RequestParameters, &p); err != nil {
		return ""
	}
	return p.VersionID
}

// UserIdentity is the caller of an event. For a call made with a role's
// session, Type is "AssumedRole" and SessionContext.SessionIssuer.ARN is the
// role's ARN (Arn is the session's arn:aws:sts::…:assumed-role/… ARN).
type UserIdentity struct {
	Type           string         `json:"type"`
	ARN            string         `json:"arn"`
	InvokedBy      string         `json:"invokedBy"`
	SessionContext SessionContext `json:"sessionContext"`
}

// SessionContext holds the issuer of a temporary session.
type SessionContext struct {
	SessionIssuer SessionIssuer `json:"sessionIssuer"`
}

// SessionIssuer is the role (or user) that issued the session.
type SessionIssuer struct {
	Type string `json:"type"`
	ARN  string `json:"arn"`
}

// RoleARN returns the ARN of the role whose session made the call, or "" when
// the caller is not a role session (an IAM user, an AWS service, the account
// root, another account).
func (e *Event) RoleARN() string {
	if e.UserIdentity.Type != "AssumedRole" || e.UserIdentity.SessionContext.SessionIssuer.Type != "Role" {
		return ""
	}
	return e.UserIdentity.SessionContext.SessionIssuer.ARN
}

// IsAPICall reports whether the event is a call some principal made. Service
// events (AwsServiceEvent), Insights events and console sign-ins carry no IAM
// action of the role, so the audit skips them. A record with no eventType is
// treated as an API call: older log files and some test fixtures omit it.
func (e *Event) IsAPICall() bool {
	return e.EventType == "" || e.EventType == "AwsApiCall"
}

// errNoRecords marks a JSON document with no "Records" array.
var errNoRecords = errors.New(`no "Records" array`)

// ParseTrailStream decodes a CloudTrail log file ({"Records": [...]}, already
// decompressed) from r and calls fn for each record. It decodes one record at
// a time, so memory does not grow with the file. A record that does not
// decode is an error: a silently skipped record would hide an action from
// the audit.
func ParseTrailStream(r io.Reader, fn func(Event) error) error {
	dec := json.NewDecoder(r)
	if err := expectDelim(dec, '{'); err != nil {
		return fmt.Errorf("decode CloudTrail log file: %w", err)
	}
	found := false
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("decode CloudTrail log file: %w", err)
		}
		if key, _ := tok.(string); key != "Records" {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return fmt.Errorf("decode CloudTrail log file: %w", err)
			}
			continue
		}
		found = true
		if err := expectDelim(dec, '['); err != nil {
			return fmt.Errorf("decode CloudTrail log file: Records: %w", err)
		}
		for i := 0; dec.More(); i++ {
			var ev Event
			if err := dec.Decode(&ev); err != nil {
				return fmt.Errorf("decode record %d: %w", i, err)
			}
			if err := fn(ev); err != nil {
				return err
			}
		}
		if err := expectDelim(dec, ']'); err != nil {
			return fmt.Errorf("decode CloudTrail log file: Records: %w", err)
		}
	}
	if !found {
		return errNoRecords
	}
	return nil
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return fmt.Errorf("want %q, got %v", want, tok)
	}
	return nil
}

// ParseEvent decodes one CloudTrail record, such as the CloudTrailEvent
// string of a LookupEvents result.
func ParseEvent(data []byte) (Event, error) {
	var ev Event
	if err := json.Unmarshal(data, &ev); err != nil {
		return Event{}, fmt.Errorf("decode CloudTrail event: %w", err)
	}
	return ev, nil
}
