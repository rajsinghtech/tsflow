package services

import (
	"fmt"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

func BenchmarkRollingCacheNodePairs(b *testing.B) {
	now := time.Now().UTC().Truncate(time.Minute).Unix()
	for _, n := range []int{2000, 5000, 20000} {
		pairs := benchmarkPairs(now, n)
		b.Run(fmt.Sprintf("linear_insert_%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				cache := NewRollingWindowCache(time.Hour)
				cache.updateLinear(pairs, nil, nil, nil)
			}
		})
		b.Run(fmt.Sprintf("indexed_insert_%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				cache := NewRollingWindowCache(time.Hour)
				cache.Update(pairs, nil, nil, nil)
			}
		})
		b.Run(fmt.Sprintf("linear_merge_%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				cache := NewRollingWindowCache(time.Hour)
				cache.updateLinear(pairs, nil, nil, nil)
				b.StartTimer()
				cache.updateLinear(pairs, nil, nil, nil)
			}
		})
		b.Run(fmt.Sprintf("indexed_merge_%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				cache := NewRollingWindowCache(time.Hour)
				cache.Update(pairs, nil, nil, nil)
				b.StartTimer()
				cache.Update(pairs, nil, nil, nil)
			}
		})
	}
}

func benchmarkPairs(bucket int64, n int) []database.NodePairAggregate {
	pairs := make([]database.NodePairAggregate, n)
	for i := 0; i < n; i++ {
		pairs[i] = database.NodePairAggregate{
			Bucket: bucket, SrcNodeID: fmt.Sprintf("n%08dCNTRL", i), DstNodeID: fmt.Sprintf("n%08dCNTRL", (i+1)%n),
			TrafficType: "virtual", TxBytes: 1000, RxBytes: 400, TxPkts: 10, RxPkts: 4, FlowCount: 1,
			Protocols: "[6]", ProtocolBytes: `{"6":1400}`,
			Ports:           `[{"port":443,"proto":6,"bytes":1400}]`,
			TxPorts:         `[{"port":443,"proto":6,"bytes":1000}]`,
			RxPorts:         `[{"port":443,"proto":6,"bytes":400}]`,
			TxProtocolBytes: `{"6":1000}`, RxProtocolBytes: `{"6":400}`,
			DirectionalPorts: true,
		}
	}
	return pairs
}
