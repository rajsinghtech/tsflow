package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the audit database. It is a separate SQLite file from the flow
// database, so an audit insert never waits behind a long flow commit and
// audit retention is independent of flow retention.
type Store struct {
	db     *sql.DB
	writer *sql.DB
	path   string
	mu     sync.Mutex
}

// ErrNotFound is returned when an event id does not exist.
var ErrNotFound = errors.New("audit event not found")

// Open opens (and creates) the audit database at path.
func Open(path string) (*Store, error) {
	reader, err := openDB(path, 4)
	if err != nil {
		return nil, err
	}
	writer, err := openDB(path, 1)
	if err != nil {
		reader.Close()
		return nil, err
	}
	return &Store{db: reader, writer: writer, path: path}, nil
}

func openDB(path string, maxOpen int) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open audit database: %w", err)
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxOpen)
	db.SetConnMaxLifetime(time.Hour)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open audit database: %w", err)
	}
	return db, nil
}

// Path is the database file.
func (s *Store) Path() string { return s.path }

// Close closes both pools.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	err := s.writer.Close()
	if rerr := s.db.Close(); err == nil {
		err = rerr
	}
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS audit_events (
	id               INTEGER PRIMARY KEY,
	tailnet_id       TEXT NOT NULL,
	event_time       INTEGER NOT NULL,
	event_group_id   TEXT NOT NULL DEFAULT '',
	origin           TEXT NOT NULL DEFAULT '',
	action           TEXT NOT NULL,
	event_key        TEXT NOT NULL,
	actor_id         TEXT NOT NULL DEFAULT '',
	actor_type       TEXT NOT NULL DEFAULT '',
	actor_login      TEXT NOT NULL DEFAULT '' COLLATE NOCASE,
	actor_display    TEXT NOT NULL DEFAULT '',
	actor_tags       TEXT NOT NULL DEFAULT '',
	target_id        TEXT NOT NULL DEFAULT '',
	target_name      TEXT NOT NULL DEFAULT '' COLLATE NOCASE,
	target_type      TEXT NOT NULL DEFAULT '',
	target_property  TEXT NOT NULL DEFAULT '',
	target_ephemeral INTEGER NOT NULL DEFAULT 0,
	old_json         TEXT,
	new_json         TEXT,
	action_details   TEXT NOT NULL DEFAULT '',
	error            TEXT NOT NULL DEFAULT '',
	raw_json         TEXT NOT NULL,
	source           TEXT NOT NULL DEFAULT '',
	hash             TEXT NOT NULL,
	UNIQUE (tailnet_id, hash)
);
CREATE INDEX IF NOT EXISTS audit_events_time   ON audit_events (tailnet_id, event_time DESC, id DESC);
CREATE INDEX IF NOT EXISTS audit_events_actor  ON audit_events (tailnet_id, actor_login, event_time DESC);
CREATE INDEX IF NOT EXISTS audit_events_actid  ON audit_events (tailnet_id, actor_id, event_time DESC);
CREATE INDEX IF NOT EXISTS audit_events_target ON audit_events (tailnet_id, target_id, event_time DESC);
CREATE INDEX IF NOT EXISTS audit_events_tname  ON audit_events (tailnet_id, target_type, target_name, event_time DESC);
CREATE INDEX IF NOT EXISTS audit_events_key    ON audit_events (tailnet_id, event_key, event_time DESC);
CREATE INDEX IF NOT EXISTS audit_events_group  ON audit_events (tailnet_id, event_group_id);

CREATE TABLE IF NOT EXISTS audit_objects (
	tailnet_id    TEXT NOT NULL,
	key           TEXT NOT NULL,
	size          INTEGER NOT NULL DEFAULT 0,
	log_time      INTEGER NOT NULL DEFAULT 0,
	events        INTEGER NOT NULL DEFAULT 0,
	ingested_at   INTEGER NOT NULL,
	PRIMARY KEY (tailnet_id, key)
);

CREATE TABLE IF NOT EXISTS audit_state (
	tailnet_id TEXT NOT NULL PRIMARY KEY,
	cursor     INTEGER NOT NULL DEFAULT 0,
	updated_at INTEGER NOT NULL DEFAULT 0
);
`

// Init creates the schema. It is safe to run on every start.
func (s *Store) Init(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.writer.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create audit schema: %w", err)
	}
	return nil
}

// ObjectRecord marks a source object as read, inside the same transaction
// as its events.
type ObjectRecord struct {
	Key     string
	Size    int64
	LogTime time.Time
}

// Insert stores events for one tailnet. Records already stored (same hash)
// are skipped. When obj is set the object is recorded in the same
// transaction, so a crash never leaves an object half-read. It returns the
// number of new rows.
func (s *Store) Insert(ctx context.Context, tailnetID, source string, events []Event, obj *ObjectRecord) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO audit_events (
		tailnet_id, event_time, event_group_id, origin, action, event_key,
		actor_id, actor_type, actor_login, actor_display, actor_tags,
		target_id, target_name, target_type, target_property, target_ephemeral,
		old_json, new_json, action_details, error, raw_json, source, hash
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	inserted := 0
	for _, e := range events {
		tags := ""
		if len(e.Actor.Tags) > 0 {
			b, _ := json.Marshal(e.Actor.Tags)
			tags = string(b)
		}
		res, err := stmt.ExecContext(ctx,
			tailnetID, e.EventTime.UnixNano(), e.EventGroupID, e.Origin, e.Action, e.Event,
			e.Actor.ID, e.Actor.Type, e.Actor.LoginName, e.Actor.DisplayName, tags,
			e.Target.ID, e.Target.Name, e.Target.Type, e.Target.Property, boolInt(e.Target.IsEphemeral),
			nullString(e.Old), nullString(e.New), e.ActionDetails, e.Error, string(e.Raw), source, e.Hash,
		)
		if err != nil {
			return 0, fmt.Errorf("insert audit event: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted++
		}
	}
	if obj != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_objects (tailnet_id, key, size, log_time, events, ingested_at)
			VALUES (?,?,?,?,?,?)
			ON CONFLICT (tailnet_id, key) DO UPDATE SET events = excluded.events, ingested_at = excluded.ingested_at`,
			tailnetID, obj.Key, obj.Size, obj.LogTime.UnixNano(), len(events), time.Now().UnixNano()); err != nil {
			return 0, fmt.Errorf("record audit object: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return inserted, nil
}

// ObjectIngested reports whether key was already read for tailnetID.
func (s *Store) ObjectIngested(ctx context.Context, tailnetID, key string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM audit_objects WHERE tailnet_id = ? AND key = ?`, tailnetID, key).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Cursor returns how far the tailnet's source has read. Zero means never.
func (s *Store) Cursor(ctx context.Context, tailnetID string) (time.Time, error) {
	var ns int64
	err := s.db.QueryRowContext(ctx, `SELECT cursor FROM audit_state WHERE tailnet_id = ?`, tailnetID).Scan(&ns)
	if errors.Is(err, sql.ErrNoRows) || ns == 0 {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, ns).UTC(), nil
}

// SetCursor records how far the tailnet's source has read.
func (s *Store) SetCursor(ctx context.Context, tailnetID string, cursor time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.writer.ExecContext(ctx, `INSERT INTO audit_state (tailnet_id, cursor, updated_at) VALUES (?,?,?)
		ON CONFLICT (tailnet_id) DO UPDATE SET cursor = excluded.cursor, updated_at = excluded.updated_at`,
		tailnetID, cursor.UnixNano(), time.Now().UnixNano())
	return err
}

// Cleanup deletes events and object records older than before, for every
// tailnet. It returns the number of events deleted.
func (s *Store) Cleanup(ctx context.Context, before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.writer.ExecContext(ctx, `DELETE FROM audit_events WHERE event_time < ?`, before.UnixNano())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := s.writer.ExecContext(ctx, `DELETE FROM audit_objects WHERE log_time > 0 AND log_time < ?`, before.UnixNano()); err != nil {
		return n, err
	}
	return n, nil
}

// TailnetStats summarizes what is stored for one tailnet.
type TailnetStats struct {
	Events     int64      `json:"events"`
	Objects    int64      `json:"objects"`
	Oldest     *time.Time `json:"oldest,omitempty"`
	Newest     *time.Time `json:"newest,omitempty"`
	LastIngest *time.Time `json:"lastIngest,omitempty"`
	Cursor     *time.Time `json:"cursor,omitempty"`
}

// Stats returns counts and time bounds for tailnetID.
func (s *Store) Stats(ctx context.Context, tailnetID string) (TailnetStats, error) {
	var out TailnetStats
	var oldest, newest sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), MIN(event_time), MAX(event_time) FROM audit_events WHERE tailnet_id = ?`, tailnetID).
		Scan(&out.Events, &oldest, &newest); err != nil {
		return out, err
	}
	out.Oldest = nsPtr(oldest)
	out.Newest = nsPtr(newest)
	var last sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), MAX(ingested_at) FROM audit_objects WHERE tailnet_id = ?`, tailnetID).
		Scan(&out.Objects, &last); err != nil {
		return out, err
	}
	out.LastIngest = nsPtr(last)
	cursor, err := s.Cursor(ctx, tailnetID)
	if err != nil {
		return out, err
	}
	if !cursor.IsZero() {
		out.Cursor = &cursor
	}
	return out, nil
}

// Filter selects events. Every list matches any of its values; different
// fields must all match.
type Filter struct {
	TailnetID string
	From, To  time.Time
	// Actors match a login or actor id exactly (ignoring case), or a
	// substring of the login or display name when prefixed with ~.
	Actors []string
	// Targets match a target id exactly, or a substring of the target name.
	Targets []string
	// TargetIDs match the target id exactly.
	TargetIDs []string
	// User matches events done by the login or done to that user.
	User string
	// Events match an event key exactly (NODE.LOGIN), or a prefix ending in
	// * (NODE.*).
	Events      []string
	Actions     []string
	TargetTypes []string
	Origins     []string
	FailedOnly  bool
	// Query is free text matched against actor, target, event and details.
	Query  string
	Cursor string
	Limit  int
}

// Page is one page of events, newest first.
type Page struct {
	Events     []Event `json:"events"`
	NextCursor string  `json:"nextCursor,omitempty"`
}

const (
	DefaultPageSize = 200
	MaxPageSize     = 1000
)

const eventColumns = `id, tailnet_id, event_time, event_group_id, origin, action, event_key,
	actor_id, actor_type, actor_login, actor_display, actor_tags,
	target_id, target_name, target_type, target_property, target_ephemeral,
	old_json, new_json, action_details, error`

func (f Filter) where() (string, []any, error) {
	clauses := []string{"tailnet_id = ?"}
	args := []any{f.TailnetID}
	if !f.From.IsZero() {
		clauses = append(clauses, "event_time >= ?")
		args = append(args, f.From.UnixNano())
	}
	if !f.To.IsZero() {
		clauses = append(clauses, "event_time <= ?")
		args = append(args, f.To.UnixNano())
	}
	if vals := clean(f.Actors); len(vals) > 0 {
		var ors []string
		for _, v := range vals {
			if strings.HasPrefix(v, "~") {
				like := "%" + escapeLike(strings.TrimPrefix(v, "~")) + "%"
				ors = append(ors, `(actor_login LIKE ? ESCAPE '\' OR actor_display LIKE ? ESCAPE '\')`)
				args = append(args, like, like)
				continue
			}
			ors = append(ors, "(actor_login = ? OR actor_id = ?)")
			args = append(args, v, v)
		}
		clauses = append(clauses, "("+strings.Join(ors, " OR ")+")")
	}
	if vals := clean(f.Targets); len(vals) > 0 {
		var ors []string
		for _, v := range vals {
			ors = append(ors, `(target_id = ? OR target_name LIKE ? ESCAPE '\')`)
			args = append(args, v, "%"+escapeLike(v)+"%")
		}
		clauses = append(clauses, "("+strings.Join(ors, " OR ")+")")
	}
	if vals := clean(f.TargetIDs); len(vals) > 0 {
		clauses = append(clauses, "target_id IN ("+placeholders(len(vals))+")")
		for _, v := range vals {
			args = append(args, v)
		}
	}
	if user := strings.TrimSpace(f.User); user != "" {
		clauses = append(clauses, "(actor_login = ? OR (target_type = 'USER' AND (target_name = ? OR target_id = ?)))")
		args = append(args, user, user, user)
	}
	if vals := clean(f.Events); len(vals) > 0 {
		var ors []string
		for _, v := range vals {
			v = strings.ToUpper(v)
			if strings.HasSuffix(v, "*") {
				ors = append(ors, `event_key LIKE ? ESCAPE '\'`)
				args = append(args, escapeLike(strings.TrimSuffix(v, "*"))+"%")
				continue
			}
			ors = append(ors, "event_key = ?")
			args = append(args, v)
		}
		clauses = append(clauses, "("+strings.Join(ors, " OR ")+")")
	}
	for _, set := range []struct {
		col  string
		vals []string
	}{{"action", f.Actions}, {"target_type", f.TargetTypes}, {"origin", f.Origins}} {
		vals := clean(set.vals)
		if len(vals) == 0 {
			continue
		}
		clauses = append(clauses, set.col+" IN ("+placeholders(len(vals))+")")
		for _, v := range vals {
			args = append(args, strings.ToUpper(v))
		}
	}
	if f.FailedOnly {
		clauses = append(clauses, "error != ''")
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + escapeLike(q) + "%"
		clauses = append(clauses, `(actor_login LIKE ? ESCAPE '\' OR actor_display LIKE ? ESCAPE '\' OR target_name LIKE ? ESCAPE '\' OR target_id = ? OR event_key LIKE ? ESCAPE '\' OR action_details LIKE ? ESCAPE '\')`)
		args = append(args, like, like, like, q, like, like)
	}
	if f.Cursor != "" {
		ts, id, err := ParseCursor(f.Cursor)
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, "(event_time < ? OR (event_time = ? AND id < ?))")
		args = append(args, ts, ts, id)
	}
	return strings.Join(clauses, " AND "), args, nil
}

// Query returns one page of matching events, newest first. Paging is keyset
// on (event_time, id), so later pages stay fast and stable while new events
// arrive.
func (s *Store) Query(ctx context.Context, f Filter) (Page, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultPageSize
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}
	where, args, err := f.where()
	if err != nil {
		return Page{}, err
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, "SELECT "+eventColumns+" FROM audit_events WHERE "+where+
		" ORDER BY event_time DESC, id DESC LIMIT ?", args...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	events, err := scanEvents(rows)
	if err != nil {
		return Page{}, err
	}
	page := Page{Events: events}
	if len(events) > limit {
		page.Events = events[:limit]
		last := page.Events[limit-1]
		page.NextCursor = FormatCursor(last.EventTime.UnixNano(), last.ID)
	}
	if page.Events == nil {
		page.Events = []Event{}
	}
	return page, nil
}

// Get returns one event with its old and new values.
func (s *Store) Get(ctx context.Context, tailnetID string, id int64) (Event, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+eventColumns+" FROM audit_events WHERE tailnet_id = ? AND id = ?", tailnetID, id)
	if err != nil {
		return Event{}, err
	}
	defer rows.Close()
	events, err := scanEvents(rows)
	if err != nil {
		return Event{}, err
	}
	if len(events) == 0 {
		return Event{}, ErrNotFound
	}
	return events[0], nil
}

// Group returns every event from one operation, oldest first.
func (s *Store) Group(ctx context.Context, tailnetID, groupID string) ([]Event, error) {
	if strings.TrimSpace(groupID) == "" {
		return []Event{}, nil
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+eventColumns+" FROM audit_events WHERE tailnet_id = ? AND event_group_id = ? ORDER BY event_time, id LIMIT 500", tailnetID, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events, err := scanEvents(rows)
	if events == nil {
		events = []Event{}
	}
	return events, err
}

// Count is one facet value.
type Count struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
	Count int64  `json:"count"`
}

// Facets are value counts for the filter menus.
type Facets struct {
	Events      []Count `json:"events"`
	Actors      []Count `json:"actors"`
	TargetTypes []Count `json:"targetTypes"`
	Origins     []Count `json:"origins"`
	Total       int64   `json:"total"`
}

// Facets counts events matching f by event, actor, target type and origin.
// Each menu ignores its own filter, so picking one event still shows the
// counts for the others. Total uses the whole filter. Cursor and Limit are
// ignored.
func (s *Store) Facets(ctx context.Context, f Filter) (Facets, error) {
	f.Cursor, f.Limit = "", 0
	var out Facets
	where, args, err := f.where()
	if err != nil {
		return out, err
	}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_events WHERE "+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	facet := func(scope Filter, expr, label string, limit int) ([]Count, error) {
		where, args, err := scope.where()
		if err != nil {
			return nil, err
		}
		q := "SELECT " + expr + ", " + label + ", COUNT(*) AS n FROM audit_events WHERE " + where +
			" GROUP BY " + expr + " ORDER BY n DESC, 1 LIMIT " + strconv.Itoa(limit)
		rows, err := s.db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		counts := []Count{}
		for rows.Next() {
			var c Count
			if err := rows.Scan(&c.Value, &c.Label, &c.Count); err != nil {
				return nil, err
			}
			if c.Label == c.Value {
				c.Label = ""
			}
			counts = append(counts, c)
		}
		return counts, rows.Err()
	}
	without := func(clear func(*Filter)) Filter {
		g := f
		clear(&g)
		return g
	}
	if out.Events, err = facet(without(func(g *Filter) { g.Events = nil }), "event_key", "event_key", 200); err != nil {
		return out, err
	}
	if out.Actors, err = facet(without(func(g *Filter) { g.Actors = nil }), "CASE WHEN actor_login != '' THEN actor_login ELSE actor_type END", "MAX(actor_display)", 100); err != nil {
		return out, err
	}
	if out.TargetTypes, err = facet(without(func(g *Filter) { g.TargetTypes = nil }), "target_type", "target_type", 50); err != nil {
		return out, err
	}
	if out.Origins, err = facet(without(func(g *Filter) { g.Origins = nil }), "origin", "origin", 50); err != nil {
		return out, err
	}
	return out, nil
}

// LoginRef is one login event in a user summary.
type LoginRef struct {
	EventID   int64     `json:"eventId"`
	Kind      string    `json:"kind"`
	EventTime time.Time `json:"eventTime"`
	Target    string    `json:"target,omitempty"`
	TargetID  string    `json:"targetId,omitempty"`
	Origin    string    `json:"origin,omitempty"`
}

// NodeRef is a device the user signed in to or changed.
type NodeRef struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	LastEvent time.Time  `json:"lastEvent"`
	LastLogin *time.Time `json:"lastLogin,omitempty"`
	Events    int64      `json:"events"`
}

// UserSummary is what the audit log says about one login.
//
// LastLogin is the newest of the three login kinds. A node login only
// happens when a device signs in or re-authenticates (often months apart),
// so it is not the same as "last used the network". Pair it with the live
// users API lastSeen and with flow logs.
type UserSummary struct {
	Login            string     `json:"login"`
	DisplayName      string     `json:"displayName,omitempty"`
	LastLogin        *LoginRef  `json:"lastLogin,omitempty"`
	LastNodeLogin    *LoginRef  `json:"lastNodeLogin,omitempty"`
	LastConsoleLogin *LoginRef  `json:"lastConsoleLogin,omitempty"`
	LastWebLogin     *LoginRef  `json:"lastWebLogin,omitempty"`
	LastActivity     *time.Time `json:"lastActivity,omitempty"`
	FirstSeen        *time.Time `json:"firstSeen,omitempty"`
	ActorEvents      int64      `json:"actorEvents"`
	TargetEvents     int64      `json:"targetEvents"`
	Nodes            []NodeRef  `json:"nodes"`
}

// UserSummary builds the last-login summary for login on tailnetID.
func (s *Store) UserSummary(ctx context.Context, tailnetID, login string) (UserSummary, error) {
	login = strings.TrimSpace(login)
	out := UserSummary{Login: login, Nodes: []NodeRef{}}
	for _, kind := range []struct {
		targetType string
		dst        **LoginRef
		name       string
	}{
		{"NODE", &out.LastNodeLogin, LoginNode},
		{"ADMIN_CONSOLE", &out.LastConsoleLogin, LoginAdminConsole},
		{"WEB_INTERFACE", &out.LastWebLogin, LoginWeb},
	} {
		var ref LoginRef
		var ns int64
		err := s.db.QueryRowContext(ctx, `SELECT id, event_time, target_name, target_id, origin FROM audit_events
			WHERE tailnet_id = ? AND actor_login = ? AND action = 'LOGIN' AND target_type = ?
			ORDER BY event_time DESC, id DESC LIMIT 1`, tailnetID, login, kind.targetType).
			Scan(&ref.EventID, &ns, &ref.Target, &ref.TargetID, &ref.Origin)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return out, err
		}
		ref.Kind = kind.name
		ref.EventTime = time.Unix(0, ns).UTC()
		r := ref
		*kind.dst = &r
		if out.LastLogin == nil || r.EventTime.After(out.LastLogin.EventTime) {
			out.LastLogin = &r
		}
	}
	var last, first sql.NullInt64
	var display sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), MAX(event_time), MIN(event_time), MAX(actor_display) FROM audit_events
		WHERE tailnet_id = ? AND actor_login = ?`, tailnetID, login).Scan(&out.ActorEvents, &last, &first, &display); err != nil {
		return out, err
	}
	out.LastActivity = nsPtr(last)
	out.DisplayName = display.String
	var tfirst sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), MIN(event_time) FROM audit_events
		WHERE tailnet_id = ? AND target_type = 'USER' AND (target_name = ? OR target_id = ?)`, tailnetID, login, login).Scan(&out.TargetEvents, &tfirst); err != nil {
		return out, err
	}
	switch {
	case first.Valid && tfirst.Valid:
		out.FirstSeen = nsPtr(sql.NullInt64{Int64: min(first.Int64, tfirst.Int64), Valid: true})
	case first.Valid:
		out.FirstSeen = nsPtr(first)
	default:
		out.FirstSeen = nsPtr(tfirst)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT target_id, MAX(target_name), MAX(event_time),
			MAX(CASE WHEN action = 'LOGIN' THEN event_time END), COUNT(*)
		FROM audit_events WHERE tailnet_id = ? AND actor_login = ? AND target_type = 'NODE' AND target_id != ''
		GROUP BY target_id ORDER BY MAX(event_time) DESC LIMIT 50`, tailnetID, login)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var n NodeRef
		var lastEvent int64
		var lastLogin sql.NullInt64
		if err := rows.Scan(&n.ID, &n.Name, &lastEvent, &lastLogin, &n.Events); err != nil {
			return out, err
		}
		n.LastEvent = time.Unix(0, lastEvent).UTC()
		n.LastLogin = nsPtr(lastLogin)
		out.Nodes = append(out.Nodes, n)
	}
	return out, rows.Err()
}

func scanEvents(rows *sql.Rows) ([]Event, error) {
	var out []Event
	for rows.Next() {
		var e Event
		var ns int64
		var tags string
		var eph int
		var oldJSON, newJSON sql.NullString
		if err := rows.Scan(&e.ID, &e.TailnetID, &ns, &e.EventGroupID, &e.Origin, &e.Action, &e.Event,
			&e.Actor.ID, &e.Actor.Type, &e.Actor.LoginName, &e.Actor.DisplayName, &tags,
			&e.Target.ID, &e.Target.Name, &e.Target.Type, &e.Target.Property, &eph,
			&oldJSON, &newJSON, &e.ActionDetails, &e.Error); err != nil {
			return nil, err
		}
		e.EventTime = time.Unix(0, ns).UTC()
		e.Target.IsEphemeral = eph != 0
		if tags != "" {
			_ = json.Unmarshal([]byte(tags), &e.Actor.Tags)
		}
		if oldJSON.Valid {
			e.Old = json.RawMessage(oldJSON.String)
		}
		if newJSON.Valid {
			e.New = json.RawMessage(newJSON.String)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FormatCursor encodes a keyset position.
func FormatCursor(eventTimeNanos, id int64) string {
	return strconv.FormatInt(eventTimeNanos, 10) + "_" + strconv.FormatInt(id, 10)
}

// ParseCursor decodes a FormatCursor value.
func ParseCursor(cursor string) (int64, int64, error) {
	ts, id, ok := strings.Cut(strings.TrimSpace(cursor), "_")
	if !ok {
		return 0, 0, fmt.Errorf("invalid cursor")
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid cursor")
	}
	i, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid cursor")
	}
	return t, i, nil
}

func clean(vals []string) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func nsPtr(v sql.NullInt64) *time.Time {
	if !v.Valid || v.Int64 == 0 {
		return nil
	}
	t := time.Unix(0, v.Int64).UTC()
	return &t
}

func nullString(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
