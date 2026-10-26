package handlers

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

// The overview counts active nodes from the endpoints of the window's
// distinct pairs. That has to match a separate ActiveNodeIDs read for every
// filter and window, including self pairs, physical transport, aliases and
// hourly rollups.
func TestStatsOverviewNodesFromPairsMatchActiveNodeIDs(t *testing.T) {
	store := setupHandlerTestDB(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Hour).Add(-6 * time.Hour)
	rng := rand.New(rand.NewSource(3))
	nodes := []string{"nA", "nB", "nC", "1002", "100.64.0.20", "203.0.113.7", "127.3.3.40", "nSelf"}
	types := []string{"virtual", "subnet", "exit", "physical"}
	var rows []database.NodePairAggregate
	seen := map[string]bool{}
	for m := 0; m < 5*60; m += 1 + rng.Intn(4) {
		for k := 0; k < 3; k++ {
			src, dst := nodes[rng.Intn(len(nodes))], nodes[rng.Intn(len(nodes))]
			if rng.Intn(6) == 0 {
				dst = src
			}
			tt := types[rng.Intn(len(types))]
			key := fmt.Sprint(m, src, dst, tt)
			if seen[key] {
				continue
			}
			seen[key] = true
			rows = append(rows, database.NodePairAggregate{
				Bucket: base.Unix() + int64(m)*60, SrcNodeID: src, DstNodeID: dst, TrafficType: tt,
				TxBytes: 10, RxBytes: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":11}`, Ports: "[]",
			})
		}
	}
	if err := store.UpsertNodePairAggregates(ctx, database.DefaultTailnetID, rows); err != nil {
		t.Fatal(err)
	}
	// Close the first hours so wide windows read hourly rows in the middle.
	if err := store.UpdatePollState(ctx, database.DefaultTailnetID, base.Add(4*time.Hour)); err != nil {
		t.Fatal(err)
	}

	poller := services.NewPoller(nil, store, services.DefaultPollerConfig())
	poller.GetDeviceCache().Update([]services.Device{
		{ID: "nA", NodeID: "nA", LegacyID: "1002", Addresses: []string{"100.64.0.20"}},
		{ID: "nB", NodeID: "nB", Addresses: []string{"100.64.0.21"}},
	})
	h := &Handlers{store: store, poller: poller}

	windows := [][2]time.Duration{
		{0, 30 * time.Minute},
		{17 * time.Minute, 2*time.Hour + 17*time.Minute},
		{0, 5 * time.Hour},
		{-24 * time.Hour, 6 * time.Hour},
	}
	for _, w := range windows {
		start, end := base.Add(w[0]), base.Add(w[1])
		for _, filter := range [][]string{nil, {"virtual"}, {"virtual", "subnet"}, {"physical"}, types} {
			data, err := h.LoadStatsOverview(ctx, database.DefaultTailnetID, poller, start, end, filter)
			if err != nil {
				t.Fatal(err)
			}
			ids, err := store.ActiveNodeIDs(ctx, database.DefaultTailnetID, start, end, filter)
			if err != nil {
				t.Fatal(err)
			}
			resolve := memoizeResolve(func(id string) string { return h.resolveNodeID(poller, id) })
			want := countDistinctNodes(ids, resolve)
			if data.Summary.TotalNodes != want {
				t.Fatalf("%v..%v [%s]: totalNodes = %d, ActiveNodeIDs gives %d",
					w[0], w[1], strings.Join(filter, ","), data.Summary.TotalNodes, want)
			}
			if want == 0 && len(ids) == 0 && w[1]-w[0] > time.Hour {
				t.Fatalf("%v..%v [%s]: fixture has no active nodes", w[0], w[1], strings.Join(filter, ","))
			}
		}
	}
}

func TestNodesOfPairsListsSelfPairOnce(t *testing.T) {
	got := nodesOfPairs([][2]string{{"a", "b"}, {"c", "c"}})
	if strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("nodesOfPairs = %v", got)
	}
}
