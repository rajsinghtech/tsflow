package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// The derived traffic stats used to be three statements over the same rows:
// totals with a DISTINCT pair count, protocol bytes through json_each, and
// ports through json_each plus json_extract. On a large tailnet that is
// mostly SQLite parsing JSON and sorting. The single pass reads each row
// once and does that work in Go.
//
// It only handles rows in the exact shape this server writes. Anything else
// (NULL numbers, unusual JSON, an overflow, a blob, legacy protocol lists
// under hourly rows) returns errStatsSinglePass and the caller runs the SQL
// statements instead, inside the same read transaction, so results never
// depend on which path ran.
var errStatsSinglePass = errors.New("row needs the SQL stats path")

// statsSinglePassDisabled forces the SQL statements. Tests use it to compare
// both paths.
var statsSinglePassDisabled bool

const statsSinglePassCols = "src_node_id, dst_node_id, traffic_type, tx_bytes, rx_bytes, flow_count, protocol_bytes, protocols, ports, " +
	"(typeof(protocol_bytes) = 'blob' OR typeof(protocols) = 'blob' OR typeof(ports) = 'blob')"

type statsPortKey struct{ proto, port int64 }

type protoSum struct {
	proto, bytes int64
}

type statsPassBucket struct {
	virtual, exit, subnet, physical, flows int64
	pairs                                  []uint64
	protos                                 []protoSum
	ports                                  map[statsPortKey]int64
}

type statsPass struct {
	bs             int64
	countPhysProto bool
	buckets        map[int64]*statsPassBucket
	ids            map[string]uint32
	protoScratch   []int64
}

func newStatsPass(bs int64, trafficTypes []string) *statsPass {
	return &statsPass{
		bs: bs,
		// countedTrafficClause: an explicit list is used as given, an empty
		// one leaves physical out of protocol totals.
		countPhysProto: len(trafficTypes) > 0,
		buckets:        make(map[int64]*statsPassBucket),
		ids:            make(map[string]uint32),
	}
}

func (p *statsPass) intern(id []byte) uint32 {
	if n, ok := p.ids[string(id)]; ok {
		return n
	}
	n := uint32(len(p.ids))
	p.ids[string(id)] = n
	return n
}

func addInt64(a, b int64) (int64, bool) {
	c := a + b
	if (c > a) != (b > 0) {
		return 0, false
	}
	return c, true
}

// read consumes one statement. isHour marks node_pair_hours rows: their
// protocol bytes drop the null protocol key and they never take the
// protocol-list fallback. Minute rows always may.
func (p *statsPass) read(ctx context.Context, q queryRower, query string, args []any, isHour bool) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to query node pairs for traffic stats: %w", err)
	}
	defer rows.Close()
	var (
		bucket                int64
		src, dst, traffic     sql.RawBytes
		tx, rx, flows         sql.NullInt64
		protoBytes, protocols sql.RawBytes
		ports                 sql.RawBytes
		hasBlob               int64
	)
	for rows.Next() {
		if err := rows.Scan(&bucket, &src, &dst, &traffic, &tx, &rx, &flows, &protoBytes, &protocols, &ports, &hasBlob); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errStatsSinglePass
		}
		if hasBlob != 0 || !tx.Valid || !rx.Valid || !flows.Valid {
			return errStatsSinglePass
		}
		total, ok := addInt64(tx.Int64, rx.Int64)
		if !ok {
			return errStatsSinglePass
		}
		b := (bucket / p.bs) * p.bs
		st := p.buckets[b]
		if st == nil {
			st = &statsPassBucket{}
			p.buckets[b] = st
		}
		var field *int64
		physical := false
		switch string(traffic) {
		case "virtual":
			field = &st.virtual
		case "exit":
			field = &st.exit
		case "subnet":
			field = &st.subnet
		case "physical":
			field = &st.physical
			physical = true
		}
		if field != nil {
			if *field, ok = addInt64(*field, total); !ok {
				return errStatsSinglePass
			}
		}
		if st.flows, ok = addInt64(st.flows, flows.Int64); !ok {
			return errStatsSinglePass
		}
		st.pairs = append(st.pairs, uint64(p.intern(src))<<32|uint64(p.intern(dst)))

		if !physical || p.countPhysProto {
			if err := p.addProtocols(st, protoBytes, protocols, total, isHour); err != nil {
				return err
			}
		}
		// portAccountingClause: physical never ranks ports.
		if !physical {
			if err := p.addPorts(st, ports); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return rows.Close()
}

func (st *statsPassBucket) addProto(proto, bytes int64) bool {
	for i := range st.protos {
		if st.protos[i].proto == proto {
			var ok bool
			st.protos[i].bytes, ok = addInt64(st.protos[i].bytes, bytes)
			return ok
		}
	}
	st.protos = append(st.protos, protoSum{proto, bytes})
	return true
}

func (p *statsPass) addProtocols(st *statsPassBucket, protoBytes, protocols []byte, total int64, isHour bool) error {
	empty := len(protoBytes) == 0 || string(protoBytes) == "{}"
	if !empty {
		seen := p.protoScratch[:0]
		sawNull := false
		err := parseProtocolBytes(protoBytes, func(isNull bool, proto, bytes int64) bool {
			if isNull {
				// json_remove drops the null key on hourly rows. On minute
				// rows SQL casts it to protocol 0; leave that to SQL.
				if !isHour || sawNull {
					return false
				}
				sawNull = true
				return true
			}
			for _, s := range seen {
				if s == proto {
					return false // duplicate key
				}
			}
			seen = append(seen, proto)
			return st.addProto(proto, bytes)
		})
		p.protoScratch = seen
		return err
	}
	if isHour {
		return nil // hourly rows only use the list fallback on legacy plans
	}
	if len(protocols) == 0 {
		return nil // NULL or '' reads as '[]'
	}
	list := p.protoScratch[:0]
	list, ok := parseIntArray(protocols, list)
	p.protoScratch = list
	if !ok {
		return errStatsSinglePass
	}
	n := int64(len(list))
	if n == 0 {
		return nil
	}
	share := total / n
	for i, proto := range list {
		bytes := share
		if i == 0 {
			bytes += total - share*n
		}
		if !st.addProto(proto, bytes) {
			return errStatsSinglePass
		}
	}
	return nil
}

func (p *statsPass) addPorts(st *statsPassBucket, ports []byte) error {
	if len(ports) == 0 || string(ports) == "[]" {
		return nil
	}
	return parsePortList(ports, func(proto, port, bytes int64) bool {
		if st.ports == nil {
			st.ports = make(map[statsPortKey]int64)
		}
		k := statsPortKey{proto, port}
		sum, ok := addInt64(st.ports[k], bytes)
		st.ports[k] = sum
		return ok
	})
}

func (p *statsPass) results() ([]TrafficStats, error) {
	out := make([]TrafficStats, 0, len(p.buckets))
	for b, st := range p.buckets {
		ts := TrafficStats{
			Bucket:        b,
			VirtualBytes:  st.virtual,
			ExitBytes:     st.exit,
			SubnetBytes:   st.subnet,
			PhysicalBytes: st.physical,
			TotalFlows:    st.flows,
			TopPorts:      "[]",
		}
		sort.Slice(st.pairs, func(i, j int) bool { return st.pairs[i] < st.pairs[j] })
		for i, pair := range st.pairs {
			if i == 0 || pair != st.pairs[i-1] {
				ts.UniquePairs++
			}
		}
		st.pairs = nil
		// Same accumulation as the SQL path: one sum per protocol, then
		// TCP, UDP and everything else.
		for _, ps := range st.protos {
			switch ps.proto {
			case 6:
				ts.TCPBytes += ps.bytes
			case 17:
				ts.UDPBytes += ps.bytes
			default:
				ts.OtherProtoBytes += ps.bytes
			}
		}
		if len(st.ports) > 0 {
			top := make([]PortStat, 0, len(st.ports))
			for k, bytes := range st.ports {
				top = append(top, PortStat{Proto: int(k.proto), Port: int(k.port), Bytes: bytes})
			}
			sort.Slice(top, func(i, j int) bool {
				if top[i].Bytes != top[j].Bytes {
					return top[i].Bytes > top[j].Bytes
				}
				if top[i].Proto != top[j].Proto {
					return top[i].Proto < top[j].Proto
				}
				return top[i].Port < top[j].Port
			})
			if len(top) > 20 {
				top = top[:20]
			}
			encoded, err := json.Marshal(top)
			if err != nil {
				return nil, fmt.Errorf("failed to encode node pair ports: %w", err)
			}
			ts.TopPorts = string(encoded)
		}
		out = append(out, ts)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bucket < out[j].Bucket })
	return out, nil
}

// singlePassMinuteStats is the minute-row read of queryTrafficStatsFromNodePairs.
func singlePassMinuteStats(ctx context.Context, q queryRower, tailnetID string, bs int64, ranges [][2]int64, trafficTypes []string) ([]TrafficStats, error) {
	typeClause, typeArgs := trafficTypeWhereClause(trafficTypes)
	rangeSQL, rangeArgs := bucketRangePredicate("bucket", ranges)
	query := fmt.Sprintf("SELECT bucket, %s FROM node_pairs WHERE tailnet_id = ? AND %s%s", statsSinglePassCols, rangeSQL, typeClause)
	args := append([]any{tailnetID}, rangeArgs...)
	args = append(args, typeArgs...)
	pass := newStatsPass(bs, trafficTypes)
	if err := pass.read(ctx, q, query, args, false); err != nil {
		return nil, err
	}
	return pass.results()
}

// singlePassRollupStats is queryTrafficStatsFromRollup for a plan without
// legacy hours: minute edges and hourly middles, each read once.
func singlePassRollupStats(ctx context.Context, q queryRower, tailnetID string, bs int64, plan hourPlan, trafficTypes []string) ([]TrafficStats, error) {
	if plan.legacy {
		return nil, errStatsSinglePass
	}
	typeClause, typeArgs := trafficTypeWhereClause(trafficTypes)
	pass := newStatsPass(bs, trafficTypes)
	if len(plan.minutes) > 0 {
		pred, predArgs := bucketRangePredicate("bucket", plan.minutes)
		args := append([]any{tailnetID}, predArgs...)
		args = append(args, typeArgs...)
		query := fmt.Sprintf("SELECT bucket, %s FROM node_pairs WHERE tailnet_id = ? AND %s%s", statsSinglePassCols, pred, typeClause)
		if err := pass.read(ctx, q, query, args, false); err != nil {
			return nil, err
		}
	}
	if len(plan.hours) > 0 {
		pred, predArgs := bucketRangePredicate("bucket", plan.hours)
		args := append([]any{tailnetID}, predArgs...)
		args = append(args, typeArgs...)
		query := fmt.Sprintf("SELECT min_bucket, %s FROM node_pair_hours WHERE tailnet_id = ? AND %s%s", statsSinglePassCols, pred, typeClause)
		if err := pass.read(ctx, q, query, args, true); err != nil {
			return nil, err
		}
	}
	return pass.results()
}
