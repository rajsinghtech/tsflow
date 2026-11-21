// Package audit stores and queries Tailscale configuration audit logs.
//
// Configuration audit logs record changes to a tailnet: logins, device
// approvals, tag and route changes, policy file edits, keys, settings, and
// so on. They are small next to network flow logs, so they live in their own
// SQLite file and never wait on the flow writer.
package audit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Actor is who made a change.
type Actor struct {
	ID          string   `json:"id,omitempty"`
	Type        string   `json:"type,omitempty"`
	LoginName   string   `json:"loginName,omitempty"`
	DisplayName string   `json:"displayName,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// Target is what a change applied to.
type Target struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Type        string `json:"type,omitempty"`
	Property    string `json:"property,omitempty"`
	IsEphemeral bool   `json:"isEphemeral,omitempty"`
}

// Event is one configuration audit log record.
//
// Tailscale records have no unique id, so Hash (a digest of the whole
// record) is what makes ingest idempotent. ID is the local row id and is only
// stable within one database.
type Event struct {
	ID           int64     `json:"id"`
	TailnetID    string    `json:"tailnet"`
	EventTime    time.Time `json:"eventTime"`
	EventGroupID string    `json:"eventGroupID,omitempty"`
	// DeferredAt is set when Tailscale's audit rate limiter queued the
	// record to be written later. It is nil for the usual zero value.
	DeferredAt *time.Time `json:"deferredAt,omitempty"`
	Origin     string     `json:"origin,omitempty"`
	Action     string     `json:"action"`
	Event      string     `json:"event"`
	// Category groups the event for filters (logins, policy, devices...).
	Category      Category        `json:"category"`
	Actor         Actor           `json:"actor"`
	Target        Target          `json:"target"`
	Old           json.RawMessage `json:"old,omitempty"`
	New           json.RawMessage `json:"new,omitempty"`
	ActionDetails string          `json:"actionDetails,omitempty"`
	Error         string          `json:"error,omitempty"`
	Hash          string          `json:"-"`
	Raw           json.RawMessage `json:"-"`
}

// Login kinds reported by UserSummary.
const (
	LoginNode         = "node"
	LoginAdminConsole = "admin_console"
	LoginWeb          = "web_interface"
)

// LoginKind reports which kind of login an event is, or "" when it is not
// a login. A node login is a device sign-in or re-authentication. The other
// two are browser sign-ins to the admin console or the member web page.
func (e Event) LoginKind() string {
	if e.Action != "LOGIN" {
		return ""
	}
	switch e.Target.Type {
	case "NODE":
		return LoginNode
	case "ADMIN_CONSOLE":
		return LoginAdminConsole
	case "WEB_INTERFACE":
		return LoginWeb
	}
	return ""
}

// EventKey builds the TARGET.ACTION[.PROPERTY] name the Tailscale API uses
// for its event filter, such as NODE.UPDATE.ACL_TAGS or ADMIN_CONSOLE.LOGIN.
func EventKey(targetType, action, property string) string {
	parts := []string{strings.ToUpper(strings.TrimSpace(targetType)), strings.ToUpper(strings.TrimSpace(action))}
	if parts[0] == "" {
		parts[0] = "UNKNOWN"
	}
	if p := strings.ToUpper(strings.TrimSpace(property)); p != "" {
		parts = append(parts, p)
	}
	return strings.Join(parts, ".")
}

type rawRecord struct {
	EventTime     string          `json:"eventTime"`
	Type          string          `json:"type"`
	EventGroupID  string          `json:"eventGroupID"`
	DeferredAt    string          `json:"deferredAt"`
	Origin        string          `json:"origin"`
	Actor         Actor           `json:"actor"`
	Target        Target          `json:"target"`
	Action        string          `json:"action"`
	Old           json.RawMessage `json:"old"`
	New           json.RawMessage `json:"new"`
	ActionDetails string          `json:"actionDetails"`
	Error         string          `json:"error"`
}

// ErrNotAuditRecord marks a JSON value that is not a configuration log.
var ErrNotAuditRecord = errors.New("not a configuration audit record")

// ParseLine parses one line of a streamed object, or one API response body.
//
// It accepts a single record, a JSON array of records, or an object with a
// logs array (the API response shape). Blank input returns no events.
func ParseLine(line []byte) ([]Event, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}
	switch line[0] {
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(line, &items); err != nil {
			return nil, err
		}
		return parseItems(items)
	case '{':
		var probe struct {
			Logs *[]json.RawMessage `json:"logs"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			return nil, err
		}
		if probe.Logs != nil {
			return parseItems(*probe.Logs)
		}
		event, err := ParseRecord(line)
		if err != nil {
			return nil, err
		}
		return []Event{event}, nil
	}
	return nil, fmt.Errorf("unexpected JSON value starting with %q", line[0])
}

func parseItems(items []json.RawMessage) ([]Event, error) {
	out := make([]Event, 0, len(items))
	for i, item := range items {
		event, err := ParseRecord(item)
		if err != nil {
			return nil, fmt.Errorf("record %d: %w", i, err)
		}
		out = append(out, event)
	}
	return out, nil
}

// ParseRecord parses one configuration audit record.
func ParseRecord(raw []byte) (Event, error) {
	var rec rawRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return Event{}, err
	}
	if rec.Type != "" && !strings.EqualFold(rec.Type, "CONFIG") {
		return Event{}, fmt.Errorf("%w: type %q", ErrNotAuditRecord, rec.Type)
	}
	if rec.EventTime == "" || rec.Action == "" {
		return Event{}, fmt.Errorf("%w: missing eventTime or action", ErrNotAuditRecord)
	}
	when, err := time.Parse(time.RFC3339Nano, rec.EventTime)
	if err != nil {
		return Event{}, fmt.Errorf("eventTime: %w", err)
	}
	canonical, err := canonicalJSON(raw)
	if err != nil {
		return Event{}, err
	}
	sum := sha256.Sum256(canonical)
	key := EventKey(rec.Target.Type, rec.Action, rec.Target.Property)
	return Event{
		EventTime:     when.UTC(),
		EventGroupID:  rec.EventGroupID,
		DeferredAt:    nonZeroTime(rec.DeferredAt),
		Origin:        rec.Origin,
		Action:        strings.ToUpper(rec.Action),
		Event:         key,
		Category:      CategoryOf(key),
		Actor:         rec.Actor,
		Target:        rec.Target,
		Old:           nullable(rec.Old),
		New:           nullable(rec.New),
		ActionDetails: rec.ActionDetails,
		Error:         rec.Error,
		Hash:          hex.EncodeToString(sum[:]),
		Raw:           canonical,
	}, nil
}

// canonicalJSON re-encodes a record with sorted keys, so the same record
// hashes the same whether it came from a streamed object or the API.
func canonicalJSON(raw []byte) ([]byte, error) {
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func nullable(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	return trimmed
}

// nonZeroTime parses an RFC3339 time and drops Go's zero time
// (0001-01-01T00:00:00Z), which Tailscale sends for "not set".
func nonZeroTime(raw string) *time.Time {
	if raw == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || t.IsZero() || t.Year() <= 1 {
		return nil
	}
	t = t.UTC()
	return &t
}
