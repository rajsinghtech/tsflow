package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func record(t time.Time, actor, action, targetType, targetID, targetName, property string, old, new any) string {
	rec := map[string]any{
		"eventTime":    t.UTC().Format(time.RFC3339Nano),
		"type":         "CONFIG",
		"deferredAt":   "0001-01-01T00:00:00Z",
		"eventGroupID": fmt.Sprintf("g-%d", t.UnixNano()),
		"origin":       "ADMIN_CONSOLE",
		"actor":        map[string]any{"id": "u-" + actor, "type": "USER", "loginName": actor, "displayName": strings.Split(actor, "@")[0]},
		"target":       map[string]any{"id": targetID, "name": targetName, "type": targetType, "property": property},
		"action":       action,
	}
	if old != nil {
		rec["old"] = old
	}
	if new != nil {
		rec["new"] = new
	}
	b, _ := json.Marshal(rec)
	return string(b)
}

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func mustParse(t *testing.T, lines ...string) []Event {
	t.Helper()
	var out []Event
	for _, line := range lines {
		events, err := ParseLine([]byte(line))
		if err != nil {
			t.Fatalf("parse %s: %v", line, err)
		}
		out = append(out, events...)
	}
	return out
}

func TestParseLineShapes(t *testing.T) {
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	one := record(base, "ada@example.com", "LOGIN", "NODE", "nAAA", "laptop.example.ts.net", "", nil, nil)
	two := record(base.Add(time.Minute), "ada@example.com", "UPDATE", "NODE", "nAAA", "laptop.example.ts.net", "ACL_TAGS", []string{}, []string{"tag:dev"})

	single := mustParse(t, one)
	if len(single) != 1 || single[0].Event != "NODE.LOGIN" || single[0].LoginKind() != LoginNode {
		t.Fatalf("single record: %+v", single)
	}
	array := mustParse(t, "["+one+","+two+"]")
	if len(array) != 2 || array[1].Event != "NODE.UPDATE.ACL_TAGS" || string(array[1].New) != `["tag:dev"]` {
		t.Fatalf("array: %+v", array)
	}
	wrapped := mustParse(t, `{"version":"1.1","tailnetId":"example.com","logs":[`+one+`,`+two+`]}`)
	if len(wrapped) != 2 {
		t.Fatalf("api response: %d events", len(wrapped))
	}
	if wrapped[0].Hash != single[0].Hash {
		t.Fatal("the same record from a stream and the API must hash the same")
	}
	if events, err := ParseLine([]byte("   ")); err != nil || len(events) != 0 {
		t.Fatalf("blank line: %v %v", events, err)
	}
	if _, err := ParseLine([]byte(`{"type":"NETWORK","eventTime":"2026-01-01T00:00:00Z","action":"X"}`)); err == nil {
		t.Fatal("a non-CONFIG record must be rejected")
	}
	if _, err := ParseLine([]byte(`{"logged":"2026-01-01T00:00:00Z"}`)); err == nil {
		t.Fatal("a record without eventTime must be rejected")
	}
}

func TestHashIgnoresKeyOrder(t *testing.T) {
	a := `{"eventTime":"2026-03-01T00:00:00Z","action":"LOGIN","target":{"type":"NODE","id":"n1"},"actor":{"loginName":"ada@example.com"}}`
	b := `{"actor":{"loginName":"ada@example.com"},"target":{"id":"n1","type":"NODE"},"action":"LOGIN","eventTime":"2026-03-01T00:00:00Z"}`
	ea := mustParse(t, a)
	eb := mustParse(t, b)
	if ea[0].Hash != eb[0].Hash {
		t.Fatal("hash depends on key order")
	}
}

func TestInsertDedupsAndRecordsObject(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	events := mustParse(t,
		record(base, "ada@example.com", "LOGIN", "NODE", "nAAA", "laptop", "", nil, nil),
		record(base.Add(time.Second), "ada@example.com", "LOGIN", "ADMIN_CONSOLE", "", "", "", nil, nil),
	)
	n, err := s.Insert(ctx, "t1", "obj-1", events, &ObjectRecord{Key: "obj-1", LogTime: base})
	if err != nil || n != 2 {
		t.Fatalf("first insert: %d %v", n, err)
	}
	n, err = s.Insert(ctx, "t1", "api", events, nil)
	if err != nil || n != 0 {
		t.Fatalf("duplicate insert must add nothing: %d %v", n, err)
	}
	n, err = s.Insert(ctx, "t2", "api", events, nil)
	if err != nil || n != 2 {
		t.Fatalf("another tailnet keeps its own copy: %d %v", n, err)
	}
	seen, err := s.ObjectIngested(ctx, "t1", "obj-1")
	if err != nil || !seen {
		t.Fatalf("object not recorded: %v %v", seen, err)
	}
	seen, _ = s.ObjectIngested(ctx, "t2", "obj-1")
	if seen {
		t.Fatal("object record leaked across tailnets")
	}
	stats, err := s.Stats(ctx, "t1")
	if err != nil || stats.Events != 2 || stats.Objects != 1 || stats.Oldest == nil || !stats.Oldest.Equal(base) {
		t.Fatalf("stats: %+v %v", stats, err)
	}
}

func seed(t *testing.T, s *Store, base time.Time, count int) {
	t.Helper()
	var lines []string
	for i := 0; i < count; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		switch i % 4 {
		case 0:
			lines = append(lines, record(at, "ada@example.com", "LOGIN", "NODE", "nAAA", "ada-laptop.example.ts.net", "", nil, nil))
		case 1:
			lines = append(lines, record(at, "bob@example.com", "UPDATE", "TAILNET", "t1", "example.com", "ACL", "{\"acls\":[]}", "{\"acls\":[1]}"))
		case 2:
			lines = append(lines, record(at, "bob@example.com", "UPDATE", "NODE", "nBBB", "build-01.example.ts.net", "ACL_TAGS", nil, []string{"tag:ci"}))
		case 3:
			lines = append(lines, record(at, "ada@example.com", "LOGIN", "ADMIN_CONSOLE", "", "", "", nil, nil))
		}
	}
	if _, err := s.Insert(context.Background(), "t1", "test", mustParse(t, lines...), nil); err != nil {
		t.Fatal(err)
	}
}

func TestQueryFiltersAndKeysetPaging(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	seed(t, s, base, 40)

	var all []Event
	cursor := ""
	for pages := 0; ; pages++ {
		page, err := s.Query(ctx, Filter{TailnetID: "t1", Limit: 7, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page.Events...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if pages > 10 {
			t.Fatal("paging does not end")
		}
	}
	if len(all) != 40 {
		t.Fatalf("paged %d events, want 40", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i].EventTime.After(all[i-1].EventTime) {
			t.Fatal("events are not newest first")
		}
	}

	cases := []struct {
		name   string
		filter Filter
		want   int
	}{
		{"actor exact ignores case", Filter{Actors: []string{"ADA@example.com"}}, 20},
		{"actor substring", Filter{Actors: []string{"~bo"}}, 20},
		{"event exact", Filter{Events: []string{"NODE.LOGIN"}}, 10},
		{"event prefix", Filter{Events: []string{"NODE.*"}}, 20},
		{"comma separated events", Filter{Events: []string{"NODE.LOGIN,ADMIN_CONSOLE.LOGIN"}}, 20},
		{"target name substring", Filter{Targets: []string{"build-01"}}, 10},
		{"target id", Filter{TargetIDs: []string{"nAAA"}}, 10},
		{"target type", Filter{TargetTypes: []string{"tailnet"}}, 10},
		{"free text", Filter{Query: "ACL_TAGS"}, 10},
		{"time window", Filter{From: base, To: base.Add(9 * time.Minute)}, 10},
		{"failed only", Filter{FailedOnly: true}, 0},
		{"user is actor", Filter{User: "bob@example.com"}, 20},
	}
	for _, tc := range cases {
		tc.filter.TailnetID = "t1"
		tc.filter.Limit = 1000
		page, err := s.Query(ctx, tc.filter)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(page.Events) != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, len(page.Events), tc.want)
		}
	}
	if _, err := s.Query(ctx, Filter{TailnetID: "t1", Cursor: "nonsense"}); err == nil {
		t.Fatal("a bad cursor must fail")
	}
	page, _ := s.Query(ctx, Filter{TailnetID: "other"})
	if len(page.Events) != 0 {
		t.Fatal("events leaked across tailnets")
	}
}

func TestGetGroupAndFacets(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	// Two events from one operation share eventGroupID.
	a := strings.Replace(record(base, "ada@example.com", "UPDATE", "NODE", "nAAA", "laptop", "KEY_EXPIRY_TIME", "a", "b"), `"eventGroupID":"g-`+fmt.Sprint(base.UnixNano())+`"`, `"eventGroupID":"op-1"`, 1)
	b := strings.Replace(record(base.Add(time.Millisecond), "ada@example.com", "LOGIN", "NODE", "nAAA", "laptop", "", nil, nil), `"eventGroupID":"g-`+fmt.Sprint(base.Add(time.Millisecond).UnixNano())+`"`, `"eventGroupID":"op-1"`, 1)
	if _, err := s.Insert(ctx, "t1", "test", mustParse(t, a, b), nil); err != nil {
		t.Fatal(err)
	}
	page, _ := s.Query(ctx, Filter{TailnetID: "t1"})
	got, err := s.Get(ctx, "t1", page.Events[0].ID)
	if err != nil || got.Event != "NODE.LOGIN" {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := s.Get(ctx, "t2", page.Events[0].ID); err != ErrNotFound {
		t.Fatalf("get from another tailnet: %v", err)
	}
	group, err := s.Group(ctx, "t1", "op-1")
	if err != nil || len(group) != 2 || group[0].Event != "NODE.UPDATE.KEY_EXPIRY_TIME" {
		t.Fatalf("group: %+v %v", group, err)
	}
	window := Filter{TailnetID: "t1", From: base.Add(-time.Hour), To: base.Add(time.Hour)}
	facets, err := s.Facets(ctx, window)
	if err != nil || facets.Total != 2 || len(facets.Events) != 2 || facets.Actors[0].Value != "ada@example.com" || facets.Actors[0].Count != 2 {
		t.Fatalf("facets: %+v %v", facets, err)
	}
	// Picking one event narrows the total but keeps the other event's count
	// in the event menu, so a second one can still be picked.
	one := window
	one.Events = []string{facets.Events[0].Value}
	narrowed, err := s.Facets(ctx, one)
	if err != nil || narrowed.Total != 1 || len(narrowed.Events) != 2 || narrowed.Actors[0].Count != 1 {
		t.Fatalf("narrowed facets: %+v %v", narrowed, err)
	}
}

func TestUserSummary(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	lines := []string{
		record(base, "admin@example.com", "CREATE", "USER", "u-ada@example.com", "ada@example.com", "", nil, nil),
		record(base.Add(1*time.Hour), "ada@example.com", "LOGIN", "NODE", "nAAA", "ada-laptop", "", nil, nil),
		record(base.Add(2*time.Hour), "ada@example.com", "LOGIN", "NODE", "nCCC", "ada-phone", "", nil, nil),
		record(base.Add(3*time.Hour), "ada@example.com", "LOGIN", "ADMIN_CONSOLE", "", "", "", nil, nil),
		record(base.Add(4*time.Hour), "ada@example.com", "UPDATE", "NODE", "nAAA", "ada-laptop", "MACHINE_NAME", "old", "ada-laptop"),
		record(base.Add(5*time.Hour), "bob@example.com", "LOGIN", "NODE", "nBBB", "bob-laptop", "", nil, nil),
	}
	if _, err := s.Insert(ctx, "t1", "test", mustParse(t, lines...), nil); err != nil {
		t.Fatal(err)
	}
	sum, err := s.UserSummary(ctx, "t1", "Ada@Example.com")
	if err != nil {
		t.Fatal(err)
	}
	if sum.LastNodeLogin == nil || sum.LastNodeLogin.Target != "ada-phone" || !sum.LastNodeLogin.EventTime.Equal(base.Add(2*time.Hour)) {
		t.Fatalf("last node login: %+v", sum.LastNodeLogin)
	}
	if sum.LastConsoleLogin == nil || sum.LastLogin == nil || sum.LastLogin.Kind != LoginAdminConsole {
		t.Fatalf("last login should be the console login: %+v", sum.LastLogin)
	}
	if sum.LastWebLogin != nil {
		t.Fatal("no web login was recorded")
	}
	if sum.LastActivity == nil || !sum.LastActivity.Equal(base.Add(4*time.Hour)) {
		t.Fatalf("last activity: %v", sum.LastActivity)
	}
	if sum.FirstSeen == nil || !sum.FirstSeen.Equal(base) {
		t.Fatalf("first seen should include the user being created: %v", sum.FirstSeen)
	}
	if sum.ActorEvents != 4 || sum.TargetEvents != 1 || len(sum.Nodes) != 2 || sum.Nodes[0].ID != "nAAA" {
		t.Fatalf("summary counts: %+v", sum)
	}
	none, err := s.UserSummary(ctx, "t1", "nobody@example.com")
	if err != nil || none.LastLogin != nil || none.ActorEvents != 0 {
		t.Fatalf("unknown user: %+v %v", none, err)
	}
}

func TestCleanupAndCursor(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	seed(t, s, base, 10)
	if _, err := s.Insert(ctx, "t1", "k", nil, &ObjectRecord{Key: "old", LogTime: base}); err != nil {
		t.Fatal(err)
	}
	n, err := s.Cleanup(ctx, base.Add(5*time.Minute))
	if err != nil || n != 5 {
		t.Fatalf("cleanup: %d %v", n, err)
	}
	if seen, _ := s.ObjectIngested(ctx, "t1", "old"); seen {
		t.Fatal("old object record should be gone")
	}
	cursor, err := s.Cursor(ctx, "t1")
	if err != nil || !cursor.IsZero() {
		t.Fatalf("new tailnet cursor: %v %v", cursor, err)
	}
	if err := s.SetCursor(ctx, "t1", base); err != nil {
		t.Fatal(err)
	}
	cursor, _ = s.Cursor(ctx, "t1")
	if !cursor.Equal(base) {
		t.Fatalf("cursor: %v", cursor)
	}
}
