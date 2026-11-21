package audit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCatalogCoversTheAPISpec(t *testing.T) {
	for _, ev := range specEvents {
		e, ok := catalog[ev]
		if !ok {
			t.Errorf("%s has no catalog entry", ev)
			continue
		}
		if e.Label == "" || !KnownCategory(string(e.Category)) {
			t.Errorf("%s: %+v", ev, e)
		}
	}
	if got := Describe("WIDGET.FROB.THING"); got.Category != CategoryOther || got.Label != "Widget frob thing" {
		t.Fatalf("fallback: %+v", got)
	}
	if got := Describe("NODE.SPIN"); got.Category != CategoryDevices {
		t.Fatalf("target fallback: %+v", got)
	}
	if got := Describe("PAM_THING.CREATE"); got.Category != CategoryPAM {
		t.Fatalf("pam fallback: %+v", got)
	}
	if len(Catalog()) != len(catalog) {
		t.Fatal("Catalog() length")
	}
}

func rec(t time.Time, mutate func(map[string]any)) string {
	r := map[string]any{
		"eventTime": t.UTC().Format(time.RFC3339Nano), "type": "CONFIG", "deferredAt": "0001-01-01T00:00:00Z",
		"eventGroupID": "g", "origin": "ADMIN_CONSOLE",
		"actor":  map[string]any{"id": "u1", "type": "USER", "loginName": "ada@example.com"},
		"target": map[string]any{"id": "x", "type": "TAILNET"}, "action": "UPDATE",
	}
	mutate(r)
	b, _ := json.Marshal(r)
	return string(b)
}

func target(typ, id, name, prop string) map[string]any {
	return map[string]any{"type": typ, "id": id, "name": name, "property": prop}
}

func seedInsights(t *testing.T) (*Store, time.Time) {
	s := openTest(t)
	base := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	lines := []string{
		// Node lifecycle as one operation.
		rec(base.Add(1*time.Hour), func(r map[string]any) {
			r["action"], r["target"], r["origin"] = "CREATE", target("NODE", "n1", "ci-1.example.ts.net", ""), "NODE"
			r["target"].(map[string]any)["isEphemeral"] = true
			r["actor"] = map[string]any{"id": "kOAUTH", "type": "OAUTH_CLIENT", "displayName": "k8s operator"}
		}),
		rec(base.Add(1*time.Hour+time.Second), func(r map[string]any) {
			r["action"], r["target"], r["old"], r["new"] = "UPDATE", target("NODE", "n1", "ci-1.example.ts.net", "KEY_EXPIRY_TIME"), "0001-01-01T00:00:00Z", "2026-09-06T01:00:00Z"
		}),
		rec(base.Add(1*time.Hour+2*time.Second), func(r map[string]any) {
			r["action"], r["target"] = "LOGIN", target("NODE", "n1", "ci-1.example.ts.net", "")
		}),
		rec(base.Add(2*time.Hour), func(r map[string]any) {
			r["action"], r["target"] = "APPROVE", target("NODE", "n1", "ci-1.example.ts.net", "")
		}),
		rec(base.Add(3*time.Hour), func(r map[string]any) {
			r["action"], r["target"], r["old"], r["new"] = "UPDATE", target("NODE", "n1", "ci-1.example.ts.net", "ACL_TAGS"), []string{}, []string{"tag:ci"}
		}),
		rec(base.Add(4*time.Hour), func(r map[string]any) {
			r["action"], r["target"] = "DISABLE", target("NODE", "n1", "ci-1.example.ts.net", "KEY_EXPIRY")
		}),
		rec(base.Add(26*time.Hour), func(r map[string]any) {
			r["action"], r["target"] = "DELETE", target("NODE", "n1", "ci-1.example.ts.net", "")
			r["actor"] = map[string]any{"type": "AUTOMATED_WORKER", "displayName": "Tailscale service: remove ephemeral node"}
			r["target"].(map[string]any)["isEphemeral"] = true
			r["deferredAt"] = "2026-03-11T02:05:00Z"
		}),
		// Keys: an auth key that is later revoked, and an OAuth client.
		rec(base.Add(5*time.Hour), func(r map[string]any) {
			r["action"], r["target"] = "CREATE", target("API_KEY", "kA", "ci runners", "")
			r["new"] = map[string]any{"capabilities": map[string]any{"devices": map[string]any{"create": map[string]any{"reusable": true, "ephemeral": true, "preauthorized": true, "tags": []string{"tag:ci"}}}}, "expires": "2026-06-08T05:00:00Z"}
		}),
		rec(base.Add(30*time.Hour), func(r map[string]any) {
			r["action"], r["target"] = "REVOKE", target("API_KEY", "kA", "ci runners", "")
		}),
		rec(base.Add(6*time.Hour), func(r map[string]any) {
			r["action"], r["target"] = "CREATE", target("API_KEY", "kO", "operator", "")
			r["new"] = map[string]any{"scopes": []string{"devices:core", "auth_keys"}, "tags": []string{"tag:k8s-operator"}}
		}),
		// Settings and policy.
		rec(base.Add(7*time.Hour), func(r map[string]any) {
			r["action"], r["target"] = "ENABLE", target("TAILNET", "t", "example.com", "MACHINE_APPROVAL_NEEDED")
		}),
		rec(base.Add(8*time.Hour), func(r map[string]any) {
			r["action"], r["target"] = "DISABLE", target("TAILNET", "t", "example.com", "MACHINE_APPROVAL_NEEDED")
		}),
		rec(base.Add(9*time.Hour), func(r map[string]any) {
			r["action"], r["target"], r["old"], r["new"] = "UPDATE", target("TAILNET", "t", "example.com", "ACL"), "{\n  a\n  b\n}", "{\n  a\n  c\n  d\n}"
		}),
		rec(base.Add(10*time.Hour), func(r map[string]any) {
			r["action"], r["target"], r["error"] = "UPDATE", target("FAILED_REQUEST", "", "", ""), "insufficient permission"
		}),
	}
	if _, err := s.Insert(context.Background(), "t1", "test", mustParse(t, lines...), nil); err != nil {
		t.Fatal(err)
	}
	return s, base
}

func TestDeferredAtAndCategory(t *testing.T) {
	s, base := seedInsights(t)
	page, err := s.Query(context.Background(), Filter{TailnetID: "t1", Events: []string{"NODE.DELETE"}})
	if err != nil || len(page.Events) != 1 {
		t.Fatal(err, page)
	}
	e := page.Events[0]
	if e.DeferredAt == nil || !e.DeferredAt.Equal(base.Add(26*time.Hour+5*time.Minute)) || e.Category != CategoryDevices || !e.Target.IsEphemeral {
		t.Fatalf("event: %+v", e)
	}
	other, _ := s.Query(context.Background(), Filter{TailnetID: "t1", Events: []string{"NODE.LOGIN"}})
	if other.Events[0].DeferredAt != nil {
		t.Fatal("zero deferredAt must stay nil")
	}
}

func TestCategoryActorTypeEphemeralFilters(t *testing.T) {
	s, _ := seedInsights(t)
	ctx := context.Background()
	count := func(f Filter) int {
		f.TailnetID = "t1"
		page, err := s.Query(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		return len(page.Events)
	}
	if n := count(Filter{Categories: []string{"keys"}}); n != 3 {
		t.Fatalf("keys: %d", n)
	}
	if n := count(Filter{Categories: []string{"settings", "policy"}}); n != 3 {
		t.Fatalf("settings+policy: %d", n)
	}
	if n := count(Filter{Categories: []string{"billing"}}); n != 0 {
		t.Fatalf("billing: %d", n)
	}
	if n := count(Filter{ActorTypes: []string{"automated_worker", "OAUTH_CLIENT"}}); n != 2 {
		t.Fatalf("actor types: %d", n)
	}
	if n := count(Filter{EphemeralOnly: true}); n != 2 {
		t.Fatalf("ephemeral: %d", n)
	}
	if n := count(Filter{FailedOnly: true}); n != 1 {
		t.Fatalf("failed: %d", n)
	}
	facets, err := s.Facets(ctx, Filter{TailnetID: "t1", Categories: []string{"keys"}})
	if err != nil {
		t.Fatal(err)
	}
	if facets.Total != 3 || len(facets.Categories) < 5 || facets.Failed != 0 || len(facets.ActorTypes) == 0 || len(facets.Targets) != 2 {
		t.Fatalf("facets: %+v", facets)
	}
	if facets.Events[0].Label == "" {
		t.Fatal("facet events carry labels")
	}
	// Non-user actors facet by id or display name, and those values filter back.
	all, _ := s.Facets(ctx, Filter{TailnetID: "t1"})
	values := map[string]bool{}
	for _, a := range all.Actors {
		values[a.Value] = true
	}
	if !values["kOAUTH"] || !values["Tailscale service: remove ephemeral node"] {
		t.Fatalf("actor facet values: %+v", all.Actors)
	}
	if n := count(Filter{Actors: []string{"~kOAUTH"}}); n != 1 {
		t.Fatalf("actor id filter: %d", n)
	}
	if n := count(Filter{Actors: []string{"~Tailscale service: remove ephemeral node"}}); n != 1 {
		t.Fatalf("actor display filter: %d", n)
	}
}

func TestHistogram(t *testing.T) {
	s, base := seedInsights(t)
	h, err := s.Histogram(context.Background(), Filter{TailnetID: "t1", From: base, To: base.Add(48*time.Hour - time.Nanosecond)}, 24*time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if h.Bucket != "day" || len(h.Buckets) != 2 || h.Buckets[0].Total != 12 || h.Buckets[1].Total != 2 || h.Buckets[0].Failed != 1 {
		t.Fatalf("histogram: %+v", h)
	}
	if h.Buckets[0].Categories[CategoryDevices] != 5 || h.Buckets[0].Categories[CategoryLogins] != 1 || h.Buckets[0].Categories[CategoryKeys] != 2 {
		t.Fatalf("categories: %+v", h.Buckets[0].Categories)
	}
	// A -6h offset puts everything before 06:00 UTC in the day before.
	local, err := s.Histogram(context.Background(), Filter{TailnetID: "t1", From: base, To: base.Add(47 * time.Hour)}, 24*time.Hour, -6*3600)
	if err != nil {
		t.Fatal(err)
	}
	if !local.Buckets[0].Start.Equal(base.Add(-18*time.Hour)) || local.Buckets[0].Total != 7 {
		t.Fatalf("local histogram: %+v", local.Buckets[0])
	}
	auto, _ := s.Histogram(context.Background(), Filter{TailnetID: "t1", From: base, To: base.Add(12 * time.Hour)}, 0, 0)
	if auto.Bucket != "hour" || len(auto.Buckets) != 13 {
		t.Fatalf("auto bucket: %s %d", auto.Bucket, len(auto.Buckets))
	}
}

func TestKeysSettingsPolicy(t *testing.T) {
	s, _ := seedInsights(t)
	ctx := context.Background()
	keys, err := s.Keys(ctx, Filter{TailnetID: "t1"}, 0)
	if err != nil || len(keys) != 2 {
		t.Fatal(err, keys)
	}
	auth := keys[0]
	if auth.ID != "kA" || auth.Kind != "auth_key" || auth.Status != "revoked" || auth.Created == nil || auth.Revoked == nil ||
		auth.Reusable == nil || !*auth.Reusable || auth.Preauthorized == nil || strings.Join(auth.Tags, ",") != "tag:ci" || auth.Expires != "2026-06-08T05:00:00Z" {
		t.Fatalf("auth key: %+v", auth)
	}
	if keys[1].Kind != "oauth_client" || keys[1].Status != "active" || len(keys[1].Scopes) != 2 {
		t.Fatalf("oauth client: %+v", keys[1])
	}
	settings, err := s.Settings(ctx, Filter{TailnetID: "t1"})
	if err != nil || len(settings) != 1 || settings[0].State != "disabled" || settings[0].Changes != 2 || settings[0].Label != "Device approval" {
		t.Fatalf("settings: %+v %v", settings, err)
	}
	policy, err := s.PolicyChanges(ctx, Filter{TailnetID: "t1"}, 10)
	if err != nil || len(policy) != 1 || policy[0].Added != 2 || policy[0].Removed != 1 || policy[0].Actor != "ada@example.com" {
		t.Fatalf("policy: %+v %v", policy, err)
	}
}

func TestNodeSummary(t *testing.T) {
	s, base := seedInsights(t)
	n, err := s.NodeSummary(context.Background(), "t1", "n1")
	if err != nil || n == nil {
		t.Fatal(err)
	}
	if !n.Ephemeral || n.Created == nil || n.Created.Type != "OAUTH_CLIENT" || n.Approved == nil || n.LastLogin == nil ||
		n.Deleted == nil || n.Deleted.Type != "AUTOMATED_WORKER" || n.KeyExpiryDisabled == nil || !*n.KeyExpiryDisabled ||
		n.KeyExpiry == nil || !n.KeyExpiry.Equal(time.Date(2026, 9, 6, 1, 0, 0, 0, time.UTC)) || string(n.Tags) != `["tag:ci"]` || n.Events != 7 {
		t.Fatalf("summary: %+v", n)
	}
	_ = base
	none, err := s.NodeSummary(context.Background(), "t1", "nope")
	if err != nil || none != nil {
		t.Fatalf("missing node: %+v %v", none, err)
	}
}

func TestEachPagesAndKeepsRaw(t *testing.T) {
	s, _ := seedInsights(t)
	var n int
	err := s.Each(context.Background(), Filter{TailnetID: "t1"}, 0, func(e Event, raw []byte) error {
		n++
		if !strings.Contains(string(raw), `"eventTime"`) {
			t.Fatalf("raw record: %s", raw)
		}
		return nil
	})
	if err != nil || n != 14 {
		t.Fatalf("each: %d %v", n, err)
	}
	n = 0
	_ = s.Each(context.Background(), Filter{TailnetID: "t1"}, 3, func(Event, []byte) error { n++; return nil })
	if n != 3 {
		t.Fatalf("each max: %d", n)
	}
}

func TestLineDiffStats(t *testing.T) {
	if a, r := LineDiffStats("a\nb\nc", "a\nB\nc\nd"); a != 2 || r != 1 {
		t.Fatalf("%d %d", a, r)
	}
	if a, r := LineDiffStats("", "x\ny"); a != 2 || r != 0 {
		t.Fatalf("%d %d", a, r)
	}
}

func TestParseKeyDetailsKinds(t *testing.T) {
	cases := map[string]string{
		`{"keyType":"api","expires":"2026-06-01T00:00:00Z"}`:           "api_access_token",
		`{"keyType":"client","scopes":["dns"]}`:                        "oauth_client",
		`{"keyType":"federated","issuer":"https://token.example.com"}`: "federated_identity",
		`{"capabilities":{"devices":{"create":{"reusable":false}}}}`:   "auth_key",
		`{"scopes":["devices:core"]}`:                                  "oauth_client",
		`{"issuer":"https://x","subject":"repo:x"}`:                    "federated_identity",
		`{"description":"mystery"}`:                                    "",
	}
	for raw, want := range cases {
		if got := parseKeyDetails(json.RawMessage(raw)).kind; got != want {
			t.Errorf("%s: got %q want %q", raw, got, want)
		}
	}
}
