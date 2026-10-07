package database

import (
	"math/rand"
	"testing"
)

// The sum sets scan a short slice and switch to an index past smallSumsLimit
// keys. Either way every key must keep its own running sum.
func TestSumSetsMatchAMapAcrossTheIndexSwitch(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, distinct := range []int{1, smallSumsLimit - 1, smallSumsLimit, smallSumsLimit + 1, smallSumsLimit + 2, 5 * smallSumsLimit} {
		var protos protoSums
		var ports portSums
		wantProto := map[int]sumState{}
		wantPort := map[portKey]sumState{}
		var protoOrder []int
		var portOrder []portKey
		for i := 0; i < distinct*20; i++ {
			k := rng.Intn(distinct)
			// Walk the keys in first-seen order early on so the switch happens mid-stream.
			if i < distinct {
				k = i
			}
			n := int64(rng.Intn(1000))
			numeric := rng.Intn(5) != 0

			if _, ok := wantProto[k]; !ok {
				protoOrder = append(protoOrder, k)
			}
			st := wantProto[k]
			st.add(n, numeric)
			wantProto[k] = st
			protos.add(k, false, n, numeric)

			pk := portKey{port: 1000 + k, proto: k % 3, protoNull: k%7 == 0}
			if _, ok := wantPort[pk]; !ok {
				portOrder = append(portOrder, pk)
			}
			pst := wantPort[pk]
			pst.add(n, numeric)
			wantPort[pk] = pst
			ports.add(pk, n, numeric)
		}

		if len(protos.items) != len(wantProto) || len(ports.items) != len(wantPort) {
			t.Fatalf("distinct=%d: got %d proto and %d port keys, want %d and %d",
				distinct, len(protos.items), len(ports.items), len(wantProto), len(wantPort))
		}
		if indexed := protos.index != nil; indexed != (distinct > smallSumsLimit) {
			t.Fatalf("distinct=%d: proto index built=%v", distinct, indexed)
		}
		if indexed := ports.index != nil; indexed != (distinct > smallSumsLimit) {
			t.Fatalf("distinct=%d: port index built=%v", distinct, indexed)
		}
		for i, item := range protos.items {
			if item.key != protoOrder[i] || item.st != wantProto[item.key] {
				t.Fatalf("distinct=%d: proto item %d = %+v, want key %d sum %+v", distinct, i, item, protoOrder[i], wantProto[protoOrder[i]])
			}
		}
		for i, item := range ports.items {
			if item.key != portOrder[i] || item.st != wantPort[item.key] {
				t.Fatalf("distinct=%d: port item %d = %+v, want key %+v sum %+v", distinct, i, item, portOrder[i], wantPort[portOrder[i]])
			}
		}
	}
}
