package database

import (
	"context"
	"testing"
	"time"
)

func TestFlowsBetweenOmitsPhysicalUnlessRequested(t *testing.T) {
	store := setupTestDB(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{
		{Bucket: base.Unix(), SrcNodeID: "ada", DstNodeID: "bob", TrafficType: "virtual", TxBytes: 80, RxBytes: 5, FlowCount: 2, Protocols: "[6]", ProtocolBytes: `{"6":85}`, Ports: `[{"port":443,"proto":6,"bytes":80}]`},
		{Bucket: base.Unix(), SrcNodeID: "bob", DstNodeID: "ada", TrafficType: "virtual", TxBytes: 7, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":7}`, Ports: `[{"port":22,"proto":6,"bytes":7}]`},
		{Bucket: base.Unix(), SrcNodeID: "ada", DstNodeID: "bob", TrafficType: "physical", TxBytes: 400, FlowCount: 1, Protocols: "[0]", ProtocolBytes: `{"0":400}`, Ports: `[{"port":27,"proto":0,"bytes":400}]`},
		{Bucket: base.Unix(), SrcNodeID: "ada", DstNodeID: "carol", TrafficType: "virtual", TxBytes: 9, FlowCount: 1, Protocols: "[17]", ProtocolBytes: `{"17":9}`},
	}); err != nil {
		t.Fatal(err)
	}
	start, end := base, base.Add(time.Minute)
	rows, err := store.FlowsBetween(ctx, DefaultTailnetID, []string{"ada"}, []string{"bob"}, start, end, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("flows = %+v, want both virtual directions and no physical row", rows)
	}
	var sawPort443, sawReverse bool
	for _, row := range rows {
		if row.TrafficType == "physical" {
			t.Fatalf("physical row leaked: %+v", row)
		}
		if row.SrcNodeID == "ada" && row.DstNodeID == "bob" {
			if row.TxBytes != 80 || row.Ports == "" || row.Ports == "[]" {
				t.Fatalf("forward row = %+v", row)
			}
			sawPort443 = true
		}
		if row.SrcNodeID == "bob" && row.DstNodeID == "ada" && row.TxBytes == 7 {
			sawReverse = true
		}
	}
	if !sawPort443 || !sawReverse {
		t.Fatalf("flows = %+v", rows)
	}

	physical, err := store.FlowsBetween(ctx, DefaultTailnetID, []string{"ada"}, []string{"bob"}, start, end, []string{"physical"})
	if err != nil {
		t.Fatal(err)
	}
	if len(physical) != 1 || physical[0].TxBytes != 400 || physical[0].TrafficType != "physical" {
		t.Fatalf("physical flows = %+v", physical)
	}
}

func TestListDevicePeersOmitsPhysicalUnlessRequested(t *testing.T) {
	store := setupTestDB(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{
		{Bucket: base.Unix(), SrcNodeID: "ada", DstNodeID: "bob", TrafficType: "virtual", TxBytes: 10, RxBytes: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":11}`},
		{Bucket: base.Unix(), SrcNodeID: "carol", DstNodeID: "ada", TrafficType: "subnet", TxBytes: 4, FlowCount: 1, Protocols: "[17]", ProtocolBytes: `{"17":4}`},
		{Bucket: base.Unix(), SrcNodeID: "ada", DstNodeID: "bob", TrafficType: "physical", TxBytes: 500, FlowCount: 3, Protocols: "[0]", ProtocolBytes: `{"0":500}`},
	}); err != nil {
		t.Fatal(err)
	}
	start, end := base, base.Add(time.Minute)
	peers, err := store.ListDevicePeers(ctx, DefaultTailnetID, []string{"ada"}, start, end, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 2 {
		t.Fatalf("peers = %+v", peers)
	}
	if peers[0].PeerID != "bob" || peers[0].TxBytes != 10 || peers[0].RxBytes != 1 {
		t.Fatalf("top peer = %+v", peers[0])
	}
	if peers[1].PeerID != "carol" || peers[1].RxBytes != 4 || peers[1].TotalBytes != 4 {
		t.Fatalf("second peer = %+v, want carol's bytes counted as ada's receive", peers[1])
	}
	physical, err := store.ListDevicePeers(ctx, DefaultTailnetID, []string{"ada"}, start, end, []string{"physical"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(physical) != 1 || physical[0].PeerID != "bob" || physical[0].TotalBytes != 500 {
		t.Fatalf("physical peers = %+v", physical)
	}
}
