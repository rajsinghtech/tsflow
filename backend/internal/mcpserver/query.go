package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/access"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
	"github.com/rajsinghtech/tsflow/backend/internal/handlers"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

var errDeviceNotFound = errors.New("device not found")

func (s *Service) bind(v Viewer, raw string) (handlers.TailnetBinding, error) {
	if s == nil || s.h == nil {
		return handlers.TailnetBinding{}, fmt.Errorf("server is not configured")
	}
	binding, err := s.h.BindTailnet(v.Restricted, v.Ident.Allow, raw)
	if err != nil {
		var te *handlers.TailnetError
		if errors.As(err, &te) {
			return handlers.TailnetBinding{}, errors.New(te.Message)
		}
		return handlers.TailnetBinding{}, err
	}
	return binding, nil
}

func (s *Service) listTailnets(v Viewer, _ listTailnetsIn) (listTailnetsOut, error) {
	if s == nil || s.h == nil {
		return listTailnetsOut{}, fmt.Errorf("server is not configured")
	}
	items := s.h.VisibleTailnets(v.Restricted, v.Ident.Allow)
	if items == nil {
		items = []handlers.TailnetSummary{}
	}
	return listTailnetsOut{Tailnets: items}, nil
}

func (s *Service) searchDevices(ctx context.Context, v Viewer, in searchDevicesIn) (searchDevicesOut, error) {
	binding, err := s.bind(v, in.Tailnet)
	if err != nil {
		return searchDevicesOut{}, err
	}
	filter, scopeName, err := deviceFilter(v, in.Scope)
	if err != nil {
		return searchDevicesOut{}, err
	}
	limit, offset, err := parsePage(in.Limit, in.Offset)
	if err != nil {
		return searchDevicesOut{}, err
	}
	devices, err := s.devices(ctx, binding)
	if err != nil {
		log.Printf("ERROR mcp search_devices: %v", err)
		return searchDevicesOut{}, fmt.Errorf("failed to list devices")
	}
	matched := make([]deviceOut, 0)
	for _, device := range devices {
		if !filter.Matches(device.User, device.Tags) || !deviceMatchesQuery(device, in.Query) {
			continue
		}
		matched = append(matched, toDeviceOut(device))
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].Hostname != matched[j].Hostname {
			return matched[i].Hostname < matched[j].Hostname
		}
		return matched[i].ID < matched[j].ID
	})
	pageItems, more := page(matched, offset, limit)
	return searchDevicesOut{
		Tailnet: binding.ID,
		Scope:   scopeName,
		Devices: pageItems,
		Limit:   limit,
		Offset:  offset,
		Count:   len(pageItems),
		HasMore: more,
	}, nil
}

func (s *Service) getDevice(ctx context.Context, v Viewer, in getDeviceIn) (getDeviceOut, error) {
	binding, err := s.bind(v, in.Tailnet)
	if err != nil {
		return getDeviceOut{}, err
	}
	filter, scopeName, err := deviceFilter(v, in.Scope)
	if err != nil {
		return getDeviceOut{}, err
	}
	devices, err := s.devices(ctx, binding)
	if err != nil {
		log.Printf("ERROR mcp get_device: %v", err)
		return getDeviceOut{}, fmt.Errorf("failed to list devices")
	}
	device, err := findDevice(devices, filter, in.Device)
	if err != nil {
		return getDeviceOut{}, err
	}
	return getDeviceOut{Tailnet: binding.ID, Scope: scopeName, Device: toDeviceOut(device)}, nil
}

func (s *Service) topTalkers(ctx context.Context, v Viewer, in rankedIn) (talkersOut, error) {
	binding, start, end, types, sortKey, limit, offset, err := s.rankArgs(v, in)
	if err != nil {
		return talkersOut{}, err
	}
	filter, scopeName, err := deviceFilter(v, in.Scope)
	if err != nil {
		return talkersOut{}, err
	}
	rows, truncated, err := s.scanTalkers(ctx, binding.ID, start, end, types, sortKey)
	if err != nil {
		log.Printf("ERROR mcp top_talkers: %v", err)
		return talkersOut{}, fmt.Errorf("failed to query top talkers")
	}
	merged := map[string]*talkerOut{}
	var order []string
	for _, row := range rows {
		id, name, ok := s.present(filter, binding.Poller, row.NodeID)
		if !ok {
			continue
		}
		if existing, seen := merged[id]; seen {
			existing.TxBytes += row.TxBytes
			existing.RxBytes += row.RxBytes
			existing.TotalBytes += row.TotalBytes
			existing.FlowCount += row.FlowCount
			continue
		}
		merged[id] = &talkerOut{
			NodeID: id, Name: name,
			TxBytes: row.TxBytes, RxBytes: row.RxBytes, TotalBytes: row.TotalBytes, FlowCount: row.FlowCount,
		}
		order = append(order, id)
	}
	talkers := make([]talkerOut, 0, len(order))
	for _, id := range order {
		talkers = append(talkers, *merged[id])
	}
	sortTalkers(talkers, sortKey)
	paged, more := page(talkers, offset, limit)
	if paged == nil {
		paged = []talkerOut{}
	}
	return talkersOut{
		Tailnet: binding.ID, Start: start, End: end, TrafficTypes: types, Sort: sortKey,
		Limit: limit, Offset: offset, Count: len(paged), HasMore: more || truncated, Truncated: truncated,
		Scope: scopeName, Talkers: paged,
	}, nil
}

func (s *Service) topPairs(ctx context.Context, v Viewer, in rankedIn) (pairsOut, error) {
	binding, start, end, types, sortKey, limit, offset, err := s.rankArgs(v, in)
	if err != nil {
		return pairsOut{}, err
	}
	filter, scopeName, err := deviceFilter(v, in.Scope)
	if err != nil {
		return pairsOut{}, err
	}
	rows, truncated, err := s.scanPairs(ctx, binding.ID, start, end, types, sortKey)
	if err != nil {
		log.Printf("ERROR mcp top_pairs: %v", err)
		return pairsOut{}, fmt.Errorf("failed to query top pairs")
	}
	type key struct{ src, dst string }
	merged := map[key]*pairOut{}
	var order []key
	for _, row := range rows {
		srcID, srcName, srcOK := s.present(filter, binding.Poller, row.SrcNodeID)
		dstID, dstName, dstOK := s.present(filter, binding.Poller, row.DstNodeID)
		if !srcOK || !dstOK {
			continue
		}
		k := key{srcID, dstID}
		if existing, seen := merged[k]; seen {
			existing.TxBytes += row.TxBytes
			existing.RxBytes += row.RxBytes
			existing.TotalBytes += row.TotalBytes
			existing.FlowCount += row.FlowCount
			continue
		}
		merged[k] = &pairOut{
			SrcNodeID: srcID, SrcName: srcName, DstNodeID: dstID, DstName: dstName,
			TxBytes: row.TxBytes, RxBytes: row.RxBytes, TotalBytes: row.TotalBytes, FlowCount: row.FlowCount,
		}
		order = append(order, k)
	}
	pairs := make([]pairOut, 0, len(order))
	for _, k := range order {
		pairs = append(pairs, *merged[k])
	}
	sortPairs(pairs, sortKey)
	paged, more := page(pairs, offset, limit)
	if paged == nil {
		paged = []pairOut{}
	}
	return pairsOut{
		Tailnet: binding.ID, Start: start, End: end, TrafficTypes: types, Sort: sortKey,
		Limit: limit, Offset: offset, Count: len(paged), HasMore: more || truncated, Truncated: truncated,
		Scope: scopeName, Pairs: paged,
	}, nil
}

func (s *Service) flowsBetween(ctx context.Context, v Viewer, in flowsBetweenIn) (flowsOut, error) {
	binding, err := s.bind(v, in.Tailnet)
	if err != nil {
		return flowsOut{}, err
	}
	start, end, err := parseWindow(in.Start, in.End)
	if err != nil {
		return flowsOut{}, err
	}
	types, err := parseTypes(in.TrafficTypes)
	if err != nil {
		return flowsOut{}, err
	}
	limit, _, err := parsePage(in.Limit, 0)
	if err != nil {
		return flowsOut{}, err
	}
	filter, scopeName, err := deviceFilter(v, in.Scope)
	if err != nil {
		return flowsOut{}, err
	}
	if s.h.Store() == nil {
		return flowsOut{}, fmt.Errorf("database not configured")
	}
	devices, err := s.devices(ctx, binding)
	if err != nil {
		log.Printf("ERROR mcp flows_between devices: %v", err)
		return flowsOut{}, fmt.Errorf("failed to list devices")
	}
	aIDs, err := s.endpointIDs(filter, binding, devices, in.A)
	if err != nil {
		return flowsOut{}, err
	}
	bIDs, err := s.endpointIDs(filter, binding, devices, in.B)
	if err != nil {
		return flowsOut{}, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, handlers.DefaultQueryTimeout)
	defer cancel()
	rows, err := s.h.Store().FlowsBetween(queryCtx, binding.ID, aIDs, bIDs, start, end, types)
	if err != nil {
		log.Printf("ERROR mcp flows_between: %v", err)
		return flowsOut{}, fmt.Errorf("failed to query flows")
	}
	flows := make([]flowOut, 0, len(rows))
	for _, row := range rows {
		srcID, srcName, srcOK := s.present(filter, binding.Poller, row.SrcNodeID)
		dstID, dstName, dstOK := s.present(filter, binding.Poller, row.DstNodeID)
		if !srcOK || !dstOK {
			continue
		}
		flows = append(flows, flowOut{
			SrcNodeID: srcID, SrcName: srcName, DstNodeID: dstID, DstName: dstName,
			TrafficType: row.TrafficType, TxBytes: row.TxBytes, RxBytes: row.RxBytes, FlowCount: row.FlowCount,
			Protocols: parseProtocols(row.ProtocolBytes), Ports: parsePorts(row.Ports),
		})
	}
	sort.Slice(flows, func(i, j int) bool {
		left, right := flows[i].TxBytes+flows[i].RxBytes, flows[j].TxBytes+flows[j].RxBytes
		if left != right {
			return left > right
		}
		if flows[i].SrcNodeID != flows[j].SrcNodeID {
			return flows[i].SrcNodeID < flows[j].SrcNodeID
		}
		return flows[i].DstNodeID < flows[j].DstNodeID
	})
	paged, more := page(flows, 0, limit)
	return flowsOut{
		Tailnet: binding.ID, Start: start, End: end, TrafficTypes: types,
		Count: len(paged), HasMore: more, Scope: scopeName, Flows: paged,
	}, nil
}

func (s *Service) devicePeers(ctx context.Context, v Viewer, in deviceIn) (peersOut, error) {
	binding, err := s.bind(v, in.Tailnet)
	if err != nil {
		return peersOut{}, err
	}
	start, end, err := parseWindow(in.Start, in.End)
	if err != nil {
		return peersOut{}, err
	}
	types, err := parseTypes(in.TrafficTypes)
	if err != nil {
		return peersOut{}, err
	}
	limit, offset, err := parsePage(in.Limit, in.Offset)
	if err != nil {
		return peersOut{}, err
	}
	filter, scopeName, err := deviceFilter(v, in.Scope)
	if err != nil {
		return peersOut{}, err
	}
	if s.h.Store() == nil {
		return peersOut{}, fmt.Errorf("database not configured")
	}
	devices, err := s.devices(ctx, binding)
	if err != nil {
		log.Printf("ERROR mcp device_peers devices: %v", err)
		return peersOut{}, fmt.Errorf("failed to list devices")
	}
	device, err := findDevice(devices, filter, in.Device)
	if err != nil {
		return peersOut{}, err
	}
	ids := equivalentIDs(binding.Poller, device.ID, device.Addresses)
	queryCtx, cancel := context.WithTimeout(ctx, handlers.DefaultQueryTimeout)
	defer cancel()
	const peerScan = 500
	rows, err := s.h.Store().ListDevicePeers(queryCtx, binding.ID, ids, start, end, types, peerScan)
	if err != nil {
		log.Printf("ERROR mcp device_peers: %v", err)
		return peersOut{}, fmt.Errorf("failed to query device peers")
	}
	peers := make([]peerOut, 0, len(rows))
	for _, row := range rows {
		id, name, ok := s.present(filter, binding.Poller, row.PeerID)
		if !ok {
			continue
		}
		peers = append(peers, peerOut{
			NodeID: id, Name: name,
			TxBytes: row.TxBytes, RxBytes: row.RxBytes, TotalBytes: row.TotalBytes, FlowCount: row.FlowCount,
		})
	}
	paged, more := page(peers, offset, limit)
	return peersOut{
		Tailnet: binding.ID, NodeID: device.ID, Name: displayName(device),
		Start: start, End: end, TrafficTypes: types,
		Limit: limit, Offset: offset, Count: len(paged), HasMore: more || len(rows) == peerScan,
		Truncated: len(rows) == peerScan, Scope: scopeName, Peers: paged,
	}, nil
}

func (s *Service) deviceTimeline(ctx context.Context, v Viewer, in timelineIn) (timelineOut, error) {
	binding, err := s.bind(v, in.Tailnet)
	if err != nil {
		return timelineOut{}, err
	}
	start, end, err := parseWindow(in.Start, in.End)
	if err != nil {
		return timelineOut{}, err
	}
	types, err := parseTypes(in.TrafficTypes)
	if err != nil {
		return timelineOut{}, err
	}
	if len(types) == 0 {
		types = []string{"virtual", "subnet", "exit"}
	}
	filter, scopeName, err := deviceFilter(v, in.Scope)
	if err != nil {
		return timelineOut{}, err
	}
	if s.h.Store() == nil {
		return timelineOut{}, fmt.Errorf("database not configured")
	}
	devices, err := s.devices(ctx, binding)
	if err != nil {
		log.Printf("ERROR mcp device_timeline devices: %v", err)
		return timelineOut{}, fmt.Errorf("failed to list devices")
	}
	device, err := findDevice(devices, filter, in.Device)
	if err != nil {
		return timelineOut{}, err
	}
	ids := equivalentIDs(binding.Poller, device.ID, nil)
	queryCtx, cancel := context.WithTimeout(ctx, handlers.DefaultQueryTimeout)
	defer cancel()
	type accum struct {
		tx, rx int64
		byType map[string]int64
	}
	byBucket := map[int64]*accum{}
	onlyPhysical := len(types) == 1 && types[0] == "physical"
	for _, typ := range types {
		for _, id := range ids {
			rows, err := s.h.Store().GetNodeBandwidthByTrafficTypes(queryCtx, binding.ID, start, end, id, []string{typ})
			if err != nil {
				log.Printf("ERROR mcp device_timeline: %v", err)
				return timelineOut{}, fmt.Errorf("failed to query device timeline")
			}
			for _, row := range rows {
				key := row.Time.UTC().Unix()
				slot := byBucket[key]
				if slot == nil {
					slot = &accum{byType: map[string]int64{}}
					byBucket[key] = slot
				}
				bytes := row.TxBytes + row.RxBytes
				slot.byType[typ] += bytes
				if typ == "physical" && !onlyPhysical {
					continue
				}
				slot.tx += row.TxBytes
				slot.rx += row.RxBytes
			}
		}
	}
	keys := make([]int64, 0, len(byBucket))
	for key := range byBucket {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	truncated := false
	if len(keys) > maxTimelineBuckets {
		keys = keys[len(keys)-maxTimelineBuckets:]
		truncated = true
	}
	buckets := make([]timelineBucket, 0, len(keys))
	for _, key := range keys {
		slot := byBucket[key]
		byType := make(map[string]int64, len(types))
		for _, typ := range types {
			byType[typ] = slot.byType[typ]
		}
		buckets = append(buckets, timelineBucket{
			Time: time.Unix(key, 0).UTC(), TxBytes: slot.tx, RxBytes: slot.rx, BytesByType: byType,
		})
	}
	return timelineOut{
		Tailnet: binding.ID, NodeID: device.ID, Name: displayName(device),
		Start: start, End: end, TrafficTypes: types, Truncated: truncated, Scope: scopeName, Buckets: buckets,
	}, nil
}

func (s *Service) newConnections(ctx context.Context, v Viewer, in newConnectionsIn) (newConnectionsOut, error) {
	binding, err := s.bind(v, in.Tailnet)
	if err != nil {
		return newConnectionsOut{}, err
	}
	start, end, err := parseWindow(in.Start, in.End)
	if err != nil {
		return newConnectionsOut{}, err
	}
	lookback, err := parseLookback(in.Lookback)
	if err != nil {
		return newConnectionsOut{}, err
	}
	types, err := parseTypes(in.TrafficTypes)
	if err != nil {
		return newConnectionsOut{}, err
	}
	limit, offset, err := parsePage(in.Limit, in.Offset)
	if err != nil {
		return newConnectionsOut{}, err
	}
	filter, scopeName, err := deviceFilter(v, in.Scope)
	if err != nil {
		return newConnectionsOut{}, err
	}
	if s.h.Store() == nil {
		return newConnectionsOut{}, fmt.Errorf("database not configured")
	}
	// TODO: share this read with a REST new-connections handler when one exists.
	// Main has no such route, so the comparison lives with the tool for now.
	queryCtx, cancel := context.WithTimeout(ctx, handlers.AggregationQueryTimeout)
	defer cancel()
	windowPairs, err := s.h.Store().DistinctPairs(queryCtx, binding.ID, start, end, types)
	if err != nil {
		log.Printf("ERROR mcp new_connections window: %v", err)
		return newConnectionsOut{}, fmt.Errorf("failed to query new connections")
	}
	if len(windowPairs) > maxNewPairs {
		return newConnectionsOut{}, fmt.Errorf("window has too many pairs; narrow the time range")
	}
	prior, err := s.h.Store().DistinctPairs(queryCtx, binding.ID, start.Add(-lookback), start, types)
	if err != nil {
		log.Printf("ERROR mcp new_connections lookback: %v", err)
		return newConnectionsOut{}, fmt.Errorf("failed to query new connections")
	}
	if len(prior) > maxNewPairs {
		return newConnectionsOut{}, fmt.Errorf("lookback has too many pairs; shorten lookback")
	}
	seen := make(map[[2]string]struct{}, len(prior))
	for _, pair := range prior {
		seen[[2]string{s.canonical(binding.Poller, pair[0]), s.canonical(binding.Poller, pair[1])}] = struct{}{}
	}
	fresh := make([]newPairOut, 0)
	for _, pair := range windowPairs {
		srcID, srcName, srcOK := s.present(filter, binding.Poller, pair[0])
		dstID, dstName, dstOK := s.present(filter, binding.Poller, pair[1])
		if !srcOK || !dstOK {
			continue
		}
		if _, old := seen[[2]string{srcID, dstID}]; old {
			continue
		}
		fresh = append(fresh, newPairOut{SrcNodeID: srcID, SrcName: srcName, DstNodeID: dstID, DstName: dstName})
	}
	sort.Slice(fresh, func(i, j int) bool {
		if fresh[i].SrcName != fresh[j].SrcName {
			return fresh[i].SrcName < fresh[j].SrcName
		}
		if fresh[i].DstName != fresh[j].DstName {
			return fresh[i].DstName < fresh[j].DstName
		}
		if fresh[i].SrcNodeID != fresh[j].SrcNodeID {
			return fresh[i].SrcNodeID < fresh[j].SrcNodeID
		}
		return fresh[i].DstNodeID < fresh[j].DstNodeID
	})
	paged, more := page(fresh, offset, limit)
	return newConnectionsOut{
		Tailnet: binding.ID, Start: start, End: end, Lookback: lookback.String(), TrafficTypes: types,
		Limit: limit, Offset: offset, Count: len(paged), HasMore: more, Scope: scopeName, Pairs: paged,
	}, nil
}

func (s *Service) statsOverview(ctx context.Context, v Viewer, in statsOverviewIn) (statsOverviewOut, error) {
	binding, err := s.bind(v, in.Tailnet)
	if err != nil {
		return statsOverviewOut{}, err
	}
	start, end, err := parseWindow(in.Start, in.End)
	if err != nil {
		return statsOverviewOut{}, err
	}
	types, err := parseTypes(in.TrafficTypes)
	if err != nil {
		return statsOverviewOut{}, err
	}
	data, err := s.h.LoadStatsOverview(ctx, binding.ID, binding.Poller, start, end, types)
	if err != nil {
		var qe *handlers.QueryError
		if errors.As(err, &qe) && qe.Public != "" {
			return statsOverviewOut{}, errors.New(qe.Public)
		}
		log.Printf("ERROR mcp stats_overview: %v", err)
		return statsOverviewOut{}, fmt.Errorf("failed to query stats overview")
	}
	buckets := make([]overviewBucket, 0, len(data.Buckets))
	for _, bucket := range data.Buckets {
		buckets = append(buckets, overviewBucket{
			Time:            time.Unix(bucket.Bucket, 0).UTC(),
			TCPBytes:        bucket.TCPBytes,
			UDPBytes:        bucket.UDPBytes,
			OtherProtoBytes: bucket.OtherProtoBytes,
			VirtualBytes:    bucket.VirtualBytes,
			ExitBytes:       bucket.ExitBytes,
			SubnetBytes:     bucket.SubnetBytes,
			PhysicalBytes:   bucket.PhysicalBytes,
			TotalFlows:      bucket.TotalFlows,
			UniquePairs:     bucket.UniquePairs,
		})
	}
	return statsOverviewOut{
		Tailnet: binding.ID, Start: start, End: end, Source: data.Source, TrafficTypes: types,
		Summary: data.Summary, Buckets: buckets,
	}, nil
}

func (s *Service) rankArgs(v Viewer, in rankedIn) (handlers.TailnetBinding, time.Time, time.Time, []string, string, int, int, error) {
	binding, err := s.bind(v, in.Tailnet)
	if err != nil {
		return handlers.TailnetBinding{}, time.Time{}, time.Time{}, nil, "", 0, 0, err
	}
	start, end, err := parseWindow(in.Start, in.End)
	if err != nil {
		return handlers.TailnetBinding{}, time.Time{}, time.Time{}, nil, "", 0, 0, err
	}
	types, err := parseTypes(in.TrafficTypes)
	if err != nil {
		return handlers.TailnetBinding{}, time.Time{}, time.Time{}, nil, "", 0, 0, err
	}
	sortKey := database.RankSortBytes
	switch in.Sort {
	case "", database.RankSortBytes:
	case database.RankSortFlows:
		sortKey = database.RankSortFlows
	default:
		return handlers.TailnetBinding{}, time.Time{}, time.Time{}, nil, "", 0, 0, fmt.Errorf("sort must be bytes or flows")
	}
	limit, offset, err := parsePage(in.Limit, in.Offset)
	if err != nil {
		return handlers.TailnetBinding{}, time.Time{}, time.Time{}, nil, "", 0, 0, err
	}
	if s.h.Store() == nil {
		return handlers.TailnetBinding{}, time.Time{}, time.Time{}, nil, "", 0, 0, fmt.Errorf("database not configured")
	}
	return binding, start, end, types, sortKey, limit, offset, nil
}

func (s *Service) scanTalkers(ctx context.Context, tailnet string, start, end time.Time, types []string, sortKey string) ([]database.RankedTalker, bool, error) {
	queryCtx, cancel := context.WithTimeout(ctx, handlers.DefaultQueryTimeout)
	defer cancel()
	var all []database.RankedTalker
	offset := 0
	for len(all) < maxScanRows {
		pageRows, more, err := s.h.Store().ListRankedTalkers(queryCtx, tailnet, start, end, database.RankQuery{
			Limit: database.RankMaxLimit, Offset: offset, Sort: sortKey, TrafficTypes: types,
		})
		if err != nil {
			return nil, false, err
		}
		all = append(all, pageRows...)
		offset += len(pageRows)
		if !more || len(pageRows) == 0 {
			return all, false, nil
		}
	}
	return all, true, nil
}

func (s *Service) scanPairs(ctx context.Context, tailnet string, start, end time.Time, types []string, sortKey string) ([]database.RankedPair, bool, error) {
	queryCtx, cancel := context.WithTimeout(ctx, handlers.DefaultQueryTimeout)
	defer cancel()
	var all []database.RankedPair
	offset := 0
	for len(all) < maxScanRows {
		pageRows, more, err := s.h.Store().ListRankedPairs(queryCtx, tailnet, start, end, database.RankQuery{
			Limit: database.RankMaxLimit, Offset: offset, Sort: sortKey, TrafficTypes: types,
		})
		if err != nil {
			return nil, false, err
		}
		all = append(all, pageRows...)
		offset += len(pageRows)
		if !more || len(pageRows) == 0 {
			return all, false, nil
		}
	}
	return all, true, nil
}

func (s *Service) devices(ctx context.Context, binding handlers.TailnetBinding) ([]services.Device, error) {
	if binding.Poller != nil {
		if cached := binding.Poller.GetDeviceCache().Devices(); len(cached) > 0 {
			return cached, nil
		}
	}
	if binding.Service != nil && binding.Service.HasCredentials() {
		resp, err := binding.Service.GetDevicesWithContext(ctx)
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.Devices == nil {
			return []services.Device{}, nil
		}
		return resp.Devices, nil
	}
	return []services.Device{}, nil
}

func (s *Service) present(filter *access.DeviceScope, poller *services.Poller, stored string) (string, string, bool) {
	if _, ok := handlers.DERPRelayName(stored); ok {
		return stored, "DERP relay", true
	}
	canonical := stored
	if s != nil && s.h != nil {
		canonical = s.h.ResolveNodeID(poller, stored)
	}
	entry := deviceEntry(poller, canonical)
	if entry == nil {
		entry = deviceEntry(poller, stored)
	}
	if entry == nil {
		if filter != nil {
			return "", "", false
		}
		name := stored
		if s != nil && s.h != nil {
			if resolved := s.h.ResolveNodeName(poller, stored); resolved != "" {
				name = resolved
			}
		}
		return canonical, name, true
	}
	if !filter.Matches(entry.Owner, entry.Tags) {
		return "", "", false
	}
	name := entry.Hostname
	if name == "" || name == "localhost" {
		name = entry.Name
		if i := strings.Index(name, "."); i > 0 {
			name = name[:i]
		}
	}
	if name == "" {
		name = entry.ID
	}
	return entry.ID, name, true
}

func (s *Service) canonical(poller *services.Poller, stored string) string {
	if _, ok := handlers.DERPRelayName(stored); ok {
		return stored
	}
	if s != nil && s.h != nil {
		return s.h.ResolveNodeID(poller, stored)
	}
	return stored
}

func (s *Service) endpointIDs(filter *access.DeviceScope, binding handlers.TailnetBinding, devices []services.Device, raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	if strings.Contains(raw, "/") {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q", raw)
		}
		var ids []string
		for _, device := range devices {
			if !filter.Matches(device.User, device.Tags) || !deviceInPrefix(device, prefix) {
				continue
			}
			ids = append(ids, equivalentIDs(binding.Poller, device.ID, device.Addresses)...)
		}
		ids = dedupe(ids)
		if len(ids) > maxEndpointIDs {
			return nil, fmt.Errorf("CIDR matches too many devices; narrow it")
		}
		return ids, nil
	}
	device, err := findDevice(devices, filter, raw)
	if err == nil {
		return dedupe(equivalentIDs(binding.Poller, device.ID, device.Addresses)), nil
	}
	if !errors.Is(err, errDeviceNotFound) {
		return nil, err
	}
	if knownDevice(devices, raw) || filter != nil {
		return nil, errDeviceNotFound
	}
	if binding.Poller != nil {
		if resolved := binding.Poller.GetDeviceCache().ResolveIP(raw); resolved != raw {
			return dedupe(equivalentIDs(binding.Poller, resolved, []string{raw})), nil
		}
	}
	return []string{raw}, nil
}

func deviceEntry(poller *services.Poller, id string) *services.DeviceCacheEntry {
	if poller == nil || id == "" {
		return nil
	}
	if entry := poller.GetDeviceCache().GetDevice(id); entry != nil {
		return entry
	}
	return poller.GetDeviceCache().GetDeviceByIP(id)
}

func equivalentIDs(poller *services.Poller, id string, addrs []string) []string {
	var ids []string
	if poller != nil && id != "" {
		ids = append(ids, poller.GetDeviceCache().EquivalentIDs(id)...)
	} else if id != "" {
		ids = []string{id}
	}
	ids = append(ids, addrs...)
	return ids
}

func findDevice(devices []services.Device, scope *access.DeviceScope, raw string) (services.Device, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return services.Device{}, fmt.Errorf("device is required")
	}
	for _, device := range devices {
		if device.ID == raw || device.NodeID == raw || hasAddress(device, raw) {
			if !scope.Matches(device.User, device.Tags) {
				return services.Device{}, errDeviceNotFound
			}
			return device, nil
		}
	}
	var matches []services.Device
	for _, device := range devices {
		if !nameMatch(device, raw) {
			continue
		}
		if !scope.Matches(device.User, device.Tags) {
			continue
		}
		matches = append(matches, device)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return services.Device{}, fmt.Errorf("device %q is ambiguous", raw)
	}
	if knownDevice(devices, raw) {
		return services.Device{}, errDeviceNotFound
	}
	return services.Device{}, errDeviceNotFound
}

func nameMatch(device services.Device, raw string) bool {
	if strings.EqualFold(strings.TrimSpace(device.Hostname), raw) || strings.EqualFold(strings.TrimSpace(device.Name), raw) {
		return true
	}
	name := device.Name
	if i := strings.Index(name, "."); i > 0 && strings.EqualFold(name[:i], raw) {
		return true
	}
	return false
}

func knownDevice(devices []services.Device, raw string) bool {
	for _, device := range devices {
		if device.ID == raw || device.NodeID == raw || hasAddress(device, raw) || nameMatch(device, raw) {
			return true
		}
	}
	return false
}

func hasAddress(device services.Device, raw string) bool {
	for _, addr := range device.Addresses {
		if addr == raw {
			return true
		}
	}
	return false
}

func deviceInPrefix(device services.Device, prefix netip.Prefix) bool {
	for _, addr := range device.Addresses {
		ip, err := netip.ParseAddr(addr)
		if err == nil && prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func deviceMatchesQuery(device services.Device, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	fields := []string{device.ID, device.NodeID, device.Name, device.Hostname, device.User}
	fields = append(fields, device.Addresses...)
	fields = append(fields, device.Tags...)
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

func toDeviceOut(device services.Device) deviceOut {
	return deviceOut{
		ID:        device.ID,
		Name:      device.Name,
		Hostname:  device.Hostname,
		User:      device.User,
		Tags:      append([]string(nil), device.Tags...),
		Addresses: append([]string(nil), device.Addresses...),
		OS:        device.OS,
		Online:    device.Online,
		LastSeen:  device.LastSeen,
	}
}

func displayName(device services.Device) string {
	if device.Hostname != "" && device.Hostname != "localhost" {
		return device.Hostname
	}
	name := device.Name
	if i := strings.Index(name, "."); i > 0 {
		return name[:i]
	}
	if name != "" {
		return name
	}
	return device.ID
}

func sortTalkers(talkers []talkerOut, sortKey string) {
	sort.Slice(talkers, func(i, j int) bool {
		if sortKey == database.RankSortFlows && talkers[i].FlowCount != talkers[j].FlowCount {
			return talkers[i].FlowCount > talkers[j].FlowCount
		}
		if talkers[i].TotalBytes != talkers[j].TotalBytes {
			return talkers[i].TotalBytes > talkers[j].TotalBytes
		}
		return talkers[i].NodeID < talkers[j].NodeID
	})
}

func sortPairs(pairs []pairOut, sortKey string) {
	sort.Slice(pairs, func(i, j int) bool {
		if sortKey == database.RankSortFlows && pairs[i].FlowCount != pairs[j].FlowCount {
			return pairs[i].FlowCount > pairs[j].FlowCount
		}
		if pairs[i].TotalBytes != pairs[j].TotalBytes {
			return pairs[i].TotalBytes > pairs[j].TotalBytes
		}
		if pairs[i].SrcNodeID != pairs[j].SrcNodeID {
			return pairs[i].SrcNodeID < pairs[j].SrcNodeID
		}
		return pairs[i].DstNodeID < pairs[j].DstNodeID
	})
}

func parseProtocols(raw string) []protocolOut {
	var decoded map[string]int64
	if json.Unmarshal([]byte(raw), &decoded) != nil || len(decoded) == 0 {
		return nil
	}
	out := make([]protocolOut, 0, len(decoded))
	for key, bytes := range decoded {
		protocol, err := strconv.Atoi(key)
		if err != nil {
			continue
		}
		out = append(out, protocolOut{Protocol: protocol, Bytes: bytes})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Protocol < out[j].Protocol
	})
	return out
}

func parsePorts(raw string) []portOut {
	if raw == "" || raw == "[]" {
		return nil
	}
	var ports []portOut
	if json.Unmarshal([]byte(raw), &ports) != nil || len(ports) == 0 {
		return nil
	}
	sort.Slice(ports, func(i, j int) bool {
		if ports[i].Bytes != ports[j].Bytes {
			return ports[i].Bytes > ports[j].Bytes
		}
		if ports[i].Proto != ports[j].Proto {
			return ports[i].Proto < ports[j].Proto
		}
		return ports[i].Port < ports[j].Port
	})
	if len(ports) > maxPorts {
		ports = ports[:maxPorts]
	}
	return ports
}

func dedupe(ids []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
