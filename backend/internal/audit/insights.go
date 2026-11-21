package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

// Target types the derived views read.
const (
	targetAPIKey  = "API_KEY"
	targetTailnet = "TAILNET"
)

// resolve turns category names into the stored event names they cover.
// Stored names come from the data, so events Tailscale adds later are still
// placed by their target type.
func (s *Store) resolve(ctx context.Context, f *Filter) error {
	cats := clean(f.Categories)
	if len(cats) == 0 || f.categoryResolved {
		f.categoryResolved = true
		return nil
	}
	want := map[Category]bool{}
	for _, c := range cats {
		want[Category(strings.ToLower(c))] = true
	}
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT event_key FROM audit_events WHERE tailnet_id = ?", f.TailnetID)
	if err != nil {
		return err
	}
	defer rows.Close()
	f.categoryKeys = nil
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return err
		}
		if want[CategoryOf(key)] {
			f.categoryKeys = append(f.categoryKeys, key)
		}
	}
	f.categoryResolved = true
	return rows.Err()
}

// HistogramBucket counts events in one time bucket.
type HistogramBucket struct {
	Start      time.Time          `json:"start"`
	Total      int64              `json:"total"`
	Failed     int64              `json:"failed"`
	Categories map[Category]int64 `json:"categories"`
}

// Histogram is event counts over time for a filter.
type Histogram struct {
	Bucket  string            `json:"bucket"`
	Buckets []HistogramBucket `json:"buckets"`
}

const maxHistogramBuckets = 500

// Histogram counts matching events per hour or per day. Days start at local
// midnight for tzOffset (seconds east of UTC). f.From must be set; f.To
// defaults to now.
func (s *Store) Histogram(ctx context.Context, f Filter, bucket time.Duration, tzOffset int) (Histogram, error) {
	f.Cursor, f.Limit = "", 0
	if f.To.IsZero() {
		f.To = time.Now().UTC()
	}
	if f.From.IsZero() || !f.From.Before(f.To) {
		return Histogram{}, errors.New("histogram needs a start before the end")
	}
	if bucket <= 0 {
		bucket = time.Hour
		if f.To.Sub(f.From) > 3*24*time.Hour {
			bucket = 24 * time.Hour
		}
	}
	for f.To.Sub(f.From)/bucket > maxHistogramBuckets {
		bucket *= 2
	}
	if err := s.resolve(ctx, &f); err != nil {
		return Histogram{}, err
	}
	where, args, err := f.where()
	if err != nil {
		return Histogram{}, err
	}
	off := int64(tzOffset) * int64(time.Second)
	bn := int64(bucket)
	first := floorDiv(f.From.UnixNano()+off, bn)
	last := floorDiv(f.To.UnixNano()+off, bn)
	q := `SELECT (event_time + ?) / ? AS b, event_key, SUM(CASE WHEN error != '' OR target_type = 'FAILED_REQUEST' THEN 1 ELSE 0 END), COUNT(*)
		FROM audit_events WHERE ` + where + ` GROUP BY b, event_key`
	rows, err := s.db.QueryContext(ctx, q, append([]any{off, bn}, args...)...)
	if err != nil {
		return Histogram{}, err
	}
	defer rows.Close()
	buckets := make([]HistogramBucket, last-first+1)
	for i := range buckets {
		buckets[i] = HistogramBucket{Start: time.Unix(0, (first+int64(i))*bn-off).UTC(), Categories: map[Category]int64{}}
	}
	for rows.Next() {
		var b, failed, n int64
		var key string
		if err := rows.Scan(&b, &key, &failed, &n); err != nil {
			return Histogram{}, err
		}
		i := b - first
		if i < 0 || i >= int64(len(buckets)) {
			continue
		}
		buckets[i].Total += n
		buckets[i].Failed += failed
		buckets[i].Categories[CategoryOf(key)] += n
	}
	name := bucket.String()
	switch bucket {
	case time.Hour:
		name = "hour"
	case 24 * time.Hour:
		name = "day"
	}
	return Histogram{Bucket: name, Buckets: buckets}, rows.Err()
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// Who is an actor at a point in time.
type Who struct {
	At      time.Time `json:"at"`
	Actor   string    `json:"actor,omitempty"`
	Type    string    `json:"actorType,omitempty"`
	EventID int64     `json:"eventId"`
}

func whoOf(e Event) *Who {
	actor := e.Actor.LoginName
	if actor == "" {
		actor = e.Actor.DisplayName
	}
	if actor == "" && len(e.Actor.Tags) > 0 {
		actor = strings.Join(e.Actor.Tags, ",")
	}
	if actor == "" {
		actor = e.Actor.ID
	}
	return &Who{At: e.EventTime, Actor: actor, Type: e.Actor.Type, EventID: e.ID}
}

// KeyLifecycle is one API access token, auth key, OAuth client or federated
// identity as seen in the audit log. Tailscale logs all of them with target
// type API_KEY; Kind is inferred from the values logged at creation.
type KeyLifecycle struct {
	ID            string     `json:"id"`
	Name          string     `json:"name,omitempty"`
	Kind          string     `json:"kind,omitempty"`
	Status        string     `json:"status"`
	Created       *Who       `json:"created,omitempty"`
	Revoked       *Who       `json:"revoked,omitempty"`
	Expired       *Who       `json:"expired,omitempty"`
	Updates       int        `json:"updates"`
	LastEvent     time.Time  `json:"lastEvent"`
	Expires       string     `json:"expires,omitempty"`
	Tags          []string   `json:"tags,omitempty"`
	Scopes        []string   `json:"scopes,omitempty"`
	Reusable      *bool      `json:"reusable,omitempty"`
	Ephemeral     *bool      `json:"ephemeral,omitempty"`
	Preauthorized *bool      `json:"preauthorized,omitempty"`
	Description   string     `json:"description,omitempty"`
	FirstSeen     *time.Time `json:"firstSeen,omitempty"`
}

// keyDetails pulls the useful settings out of a key's logged value, wherever
// they sit in the object.
type keyDetails struct {
	kind, keyType                 string
	expires, description          string
	tags, scopes                  []string
	reusable, ephemeral, preauthd *bool
}

func parseKeyDetails(raw json.RawMessage) keyDetails {
	var d keyDetails
	if len(raw) == 0 {
		return d
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return d
	}
	seen := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, val := range t {
				lk := strings.ToLower(k)
				seen[lk] = true
				switch lk {
				case "expires", "expiry", "expiryseconds":
					if d.expires == "" {
						d.expires = scalarString(val)
					}
				case "description":
					d.description = scalarString(val)
				case "tags":
					d.tags = stringList(val)
				case "scopes":
					d.scopes = stringList(val)
				case "reusable":
					d.reusable = boolPtr(val)
				case "ephemeral":
					d.ephemeral = boolPtr(val)
				case "preauthorized":
					d.preauthd = boolPtr(val)
				case "keytype":
					d.keyType = strings.ToLower(scalarString(val))
				}
				walk(val)
			}
		case []any:
			for _, item := range t {
				walk(item)
			}
		}
	}
	walk(v)
	// The keys API names the kind in keyType; older records only hint at
	// it through their fields.
	switch d.keyType {
	case "auth":
		d.kind = "auth_key"
		return d
	case "client":
		d.kind = "oauth_client"
		return d
	case "api":
		d.kind = "api_access_token"
		return d
	case "federated":
		d.kind = "federated_identity"
		return d
	}
	switch {
	case seen["issuer"] || seen["subject"]:
		d.kind = "federated_identity"
	case seen["scopes"]:
		d.kind = "oauth_client"
	case seen["reusable"] || seen["ephemeral"] || seen["preauthorized"] || seen["devices"]:
		d.kind = "auth_key"
	case seen["capabilities"]:
		d.kind = "api_access_token"
	}
	return d
}

func scalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64, bool:
		b, _ := json.Marshal(t)
		return string(b)
	}
	return ""
}

func stringList(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func boolPtr(v any) *bool {
	b, ok := v.(bool)
	if !ok {
		return nil
	}
	return &b
}

// Keys returns the lifecycle of every key with events in the filter window,
// newest activity first.
func (s *Store) Keys(ctx context.Context, f Filter, limit int) ([]KeyLifecycle, error) {
	f.TargetTypes = []string{targetAPIKey}
	events, err := s.all(ctx, f, 5000)
	if err != nil {
		return nil, err
	}
	byID := map[string]*KeyLifecycle{}
	order := []string{}
	// all returns newest first; walk oldest first so later events win.
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		id := e.Target.ID
		if id == "" {
			id = e.Target.Name
		}
		k := byID[id]
		if k == nil {
			t := e.EventTime
			k = &KeyLifecycle{ID: id, FirstSeen: &t}
			byID[id] = k
			order = append(order, id)
		}
		if e.Target.Name != "" {
			k.Name = e.Target.Name
		}
		k.LastEvent = e.EventTime
		var d keyDetails
		switch e.Action {
		case "CREATE":
			k.Created = whoOf(e)
			d = parseKeyDetails(e.New)
		case "REVOKE", "DELETE":
			k.Revoked = whoOf(e)
			d = parseKeyDetails(e.Old)
		case "EXPIRED":
			k.Expired = whoOf(e)
		case "UPDATE":
			k.Updates++
			d = parseKeyDetails(e.New)
		}
		if d.kind != "" {
			k.Kind = d.kind
		}
		if d.expires != "" {
			k.Expires = d.expires
		}
		if d.tags != nil {
			k.Tags = d.tags
		}
		if d.scopes != nil {
			k.Scopes = d.scopes
		}
		if d.description != "" {
			k.Description = d.description
		}
		if d.reusable != nil {
			k.Reusable = d.reusable
		}
		if d.ephemeral != nil {
			k.Ephemeral = d.ephemeral
		}
		if d.preauthd != nil {
			k.Preauthorized = d.preauthd
		}
	}
	out := make([]KeyLifecycle, 0, len(order))
	for _, id := range order {
		k := byID[id]
		switch {
		case k.Revoked != nil:
			k.Status = "revoked"
		case k.Expired != nil:
			k.Status = "expired"
		case k.Created != nil:
			k.Status = "active"
		default:
			k.Status = "unknown"
		}
		out = append(out, *k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastEvent.After(out[j].LastEvent) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// SettingChange is the latest change to one tailnet-wide setting.
type SettingChange struct {
	Event    string    `json:"event"`
	Label    string    `json:"label"`
	Property string    `json:"property"`
	State    string    `json:"state"`
	Changes  int       `json:"changes"`
	Last     *Who      `json:"last"`
	Category Category  `json:"category"`
	At       time.Time `json:"at"`
}

// Settings returns the latest change per tailnet setting in the window, so
// it reads as "what is on now and who last changed it".
func (s *Store) Settings(ctx context.Context, f Filter) ([]SettingChange, error) {
	f.TargetTypes = []string{targetTailnet}
	events, err := s.all(ctx, f, 5000)
	if err != nil {
		return nil, err
	}
	byProp := map[string]*SettingChange{}
	order := []string{}
	for _, e := range events { // newest first
		prop := e.Target.Property
		if prop == "" || prop == "ACL" {
			continue
		}
		c := byProp[prop]
		if c == nil {
			state := "changed"
			switch e.Action {
			case "ENABLE":
				state = "enabled"
			case "DISABLE":
				state = "disabled"
			case "CREATE":
				state = "added"
			case "DELETE":
				state = "removed"
			case "VERIFY":
				state = "verified"
			}
			label := Describe(e.Event).Label
			for _, suffix := range []string{" enabled", " disabled"} {
				label = strings.TrimSuffix(label, suffix)
			}
			c = &SettingChange{Event: e.Event, Label: label, Property: prop, State: state, Last: whoOf(e), Category: e.Category, At: e.EventTime}
			byProp[prop] = c
			order = append(order, prop)
		}
		c.Changes++
	}
	out := make([]SettingChange, 0, len(order))
	for _, p := range order {
		out = append(out, *byProp[p])
	}
	return out, nil
}

// PolicyChange is one policy file edit with its line counts.
type PolicyChange struct {
	EventID int64     `json:"eventId"`
	At      time.Time `json:"at"`
	Actor   string    `json:"actor"`
	Origin  string    `json:"origin,omitempty"`
	Added   int       `json:"added"`
	Removed int       `json:"removed"`
	Error   string    `json:"error,omitempty"`
	Details string    `json:"details,omitempty"`
}

// PolicyChanges lists policy file edits in the window, newest first.
func (s *Store) PolicyChanges(ctx context.Context, f Filter, limit int) ([]PolicyChange, error) {
	f.Events = []string{"TAILNET.UPDATE.ACL"}
	f.Categories = nil
	events, err := s.all(ctx, f, limit)
	if err != nil {
		return nil, err
	}
	out := make([]PolicyChange, 0, len(events))
	for _, e := range events {
		added, removed := LineDiffStats(jsonText(e.Old), jsonText(e.New))
		who := whoOf(e)
		out = append(out, PolicyChange{EventID: e.ID, At: e.EventTime, Actor: who.Actor, Origin: e.Origin, Added: added, Removed: removed, Error: e.Error, Details: e.ActionDetails})
	}
	return out, nil
}

// jsonText returns a JSON string's text, or the raw JSON for other values.
func jsonText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// LineDiffStats counts added and removed lines between two texts using an
// LCS on what is left after trimming common leading and trailing lines.
// Very large remainders fall back to counting every line as changed.
func LineDiffStats(before, after string) (int, int) {
	split := func(s string) []string {
		if s == "" {
			return nil
		}
		return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	}
	a, b := split(before), split(after)
	for len(a) > 0 && len(b) > 0 && a[0] == b[0] {
		a, b = a[1:], b[1:]
	}
	for len(a) > 0 && len(b) > 0 && a[len(a)-1] == b[len(b)-1] {
		a, b = a[:len(a)-1], b[:len(b)-1]
	}
	n, m := len(a), len(b)
	if n == 0 || m == 0 || n*m > 4_000_000 {
		return m, n
	}
	prev := make([]int, m+1)
	cur := make([]int, m+1)
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
			} else if prev[j] >= cur[j-1] {
				cur[j] = prev[j]
			} else {
				cur[j] = cur[j-1]
			}
		}
		prev, cur = cur, prev
	}
	common := prev[m]
	return m - common, n - common
}

// NodeSummary is one device's history from the audit log.
type NodeSummary struct {
	ID                string          `json:"id"`
	Name              string          `json:"name,omitempty"`
	Ephemeral         bool            `json:"ephemeral"`
	Created           *Who            `json:"created,omitempty"`
	Approved          *Who            `json:"approved,omitempty"`
	Deleted           *Who            `json:"deleted,omitempty"`
	LastLogin         *Who            `json:"lastLogin,omitempty"`
	LastLogout        *Who            `json:"lastLogout,omitempty"`
	KeyExpiry         *time.Time      `json:"keyExpiry,omitempty"`
	KeyExpiryDisabled *bool           `json:"keyExpiryDisabled,omitempty"`
	KeyExpired        *Who            `json:"keyExpired,omitempty"`
	Tags              json.RawMessage `json:"tags,omitempty"`
	TagsChanged       *Who            `json:"tagsChanged,omitempty"`
	Routes            json.RawMessage `json:"routes,omitempty"`
	AutoRoutes        json.RawMessage `json:"autoApprovedRoutes,omitempty"`
	ExitNode          json.RawMessage `json:"exitNode,omitempty"`
	TailnetLockSigned *Who            `json:"tailnetLockSigned,omitempty"`
	PostureChanges    int             `json:"postureChanges"`
	Renames           int             `json:"renames"`
	Events            int             `json:"events"`
	Actors            []string        `json:"actors"`
}

// NodeSummary folds every stored event for one device into its current
// state: when it was added, approved, last logged in, its key expiry, tags
// and routes, and whether it was deleted. It is nil when nothing is stored.
func (s *Store) NodeSummary(ctx context.Context, tailnetID, nodeID string) (*NodeSummary, error) {
	events, err := s.all(ctx, Filter{TailnetID: tailnetID, TargetIDs: []string{nodeID}}, 20000)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, nil
	}
	out := &NodeSummary{ID: nodeID, Actors: []string{}}
	actors := map[string]bool{}
	for i := len(events) - 1; i >= 0; i-- { // oldest first
		e := events[i]
		out.Events++
		if e.Target.Name != "" {
			out.Name = e.Target.Name
		}
		if e.Target.IsEphemeral {
			out.Ephemeral = true
		}
		if w := whoOf(e); w.Actor != "" && !actors[w.Actor] && len(out.Actors) < 20 {
			actors[w.Actor] = true
			out.Actors = append(out.Actors, w.Actor)
		}
		switch e.Event {
		case "NODE.CREATE":
			out.Created = whoOf(e)
		case "NODE.APPROVE":
			out.Approved = whoOf(e)
		case "NODE.DELETE", "NODE.REVOKE":
			out.Deleted = whoOf(e)
		case "NODE.LOGIN":
			out.LastLogin = whoOf(e)
		case "NODE.LOGOUT":
			out.LastLogout = whoOf(e)
		case "NODE.UPDATE.KEY_EXPIRY_TIME":
			out.KeyExpiry = nonZeroTime(jsonText(e.New))
		case "NODE.DISABLE.KEY_EXPIRY":
			v := true
			out.KeyExpiryDisabled = &v
		case "NODE.ENABLE.KEY_EXPIRY":
			v := false
			out.KeyExpiryDisabled = &v
		case "NODE.EXPIRED.KEY_EXPIRY_TIME":
			out.KeyExpired = whoOf(e)
		case "NODE.UPDATE.ACL_TAGS":
			out.Tags = e.New
			out.TagsChanged = whoOf(e)
		case "NODE.UPDATE.ALLOWED_IPS":
			out.Routes = e.New
		case "NODE.UPDATE.AUTO_APPROVED_ROUTES":
			out.AutoRoutes = e.New
		case "NODE.UPDATE.EXIT_NODE":
			out.ExitNode = e.New
		case "NODE.UPDATE.TKA":
			out.TailnetLockSigned = whoOf(e)
		case "NODE.UPDATE.POSTURE_IDENTITY", "NODE.UPDATE.ATTRIBUTES", "NODE.CREATE.ATTRIBUTES", "NODE.DELETE.ATTRIBUTES":
			out.PostureChanges++
		case "NODE.UPDATE.MACHINE_NAME":
			out.Renames++
		}
	}
	return out, nil
}

// all returns up to max matching events, newest first.
func (s *Store) all(ctx context.Context, f Filter, max int) ([]Event, error) {
	var out []Event
	err := s.Each(ctx, f, max, func(e Event, _ []byte) error {
		out = append(out, e)
		return nil
	})
	return out, err
}

// Each calls fn for up to max matching events, newest first, with each
// event's original Tailscale record. It pages internally.
func (s *Store) Each(ctx context.Context, f Filter, max int, fn func(Event, []byte) error) error {
	f.Cursor = ""
	if err := s.resolve(ctx, &f); err != nil {
		return err
	}
	seen := 0
	for {
		where, args, err := f.where()
		if err != nil {
			return err
		}
		size := MaxPageSize
		if max > 0 && max-seen < size {
			size = max - seen
		}
		if size <= 0 {
			return nil
		}
		rows, err := s.db.QueryContext(ctx, "SELECT "+eventColumns+", raw_json FROM audit_events WHERE "+where+
			" ORDER BY event_time DESC, id DESC LIMIT ?", append(args, size)...)
		if err != nil {
			return err
		}
		events, raws, err := scanEventsRaw(rows)
		rows.Close()
		if err != nil {
			return err
		}
		for i, e := range events {
			if err := fn(e, raws[i]); err != nil {
				return err
			}
		}
		seen += len(events)
		if len(events) < size {
			return nil
		}
		last := events[len(events)-1]
		f.Cursor = FormatCursor(last.EventTime.UnixNano(), last.ID)
	}
}

func scanEventsRaw(rows *sql.Rows) ([]Event, [][]byte, error) {
	var events []Event
	var raws [][]byte
	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		dest := make([]any, len(cols))
		e, ptrs, finish := eventScanTargets()
		copy(dest, ptrs)
		var raw string
		dest[len(cols)-1] = &raw
		if err := rows.Scan(dest...); err != nil {
			return nil, nil, err
		}
		finish()
		events = append(events, *e)
		raws = append(raws, []byte(raw))
	}
	return events, raws, rows.Err()
}
