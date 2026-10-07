package handlers

import (
	"sort"
	"strings"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

// applyRankSearch narrows query to the nodes matching q, using the same rules
// as the traffic graph search box: tag: and ip: keep their prefixes, user@term
// searches the owner login, and anything else (including a full or partial
// email) is a case-insensitive substring of a name, owner, address, or tag.
// Matching devices contribute every id their rows may be stored under. Plain
// and ip: searches also match stored ids directly, which covers addresses that
// are not devices (subnet and exit destinations).
//
// It returns false when q matches nothing, so the caller can answer with an
// empty page without querying.
func (h *Handlers) applyRankSearch(poller *services.Poller, raw string, query *database.RankQuery) bool {
	q := strings.ToLower(strings.TrimSpace(raw))
	query.Search = strings.TrimSpace(raw)
	if q == "" {
		return true
	}

	var match func(d services.Device) bool
	direct := ""
	switch {
	case strings.HasPrefix(q, "tag:"):
		term := q[len("tag:"):]
		match = func(d services.Device) bool { return anyTagContains(d.Tags, term) }
	case strings.HasPrefix(q, "ip:"):
		term := q[len("ip:"):]
		direct = term
		match = func(d services.Device) bool { return anyContains(d.Addresses, term) }
	case strings.HasPrefix(q, "user@"):
		term := q[len("user@"):]
		match = func(d services.Device) bool {
			owner := strings.ToLower(d.User)
			return owner != "" && strings.Contains(owner, term)
		}
	default:
		direct = q
		match = func(d services.Device) bool {
			return anyContains(d.Addresses, q) ||
				strings.Contains(strings.ToLower(d.Name), q) ||
				strings.Contains(strings.ToLower(d.Hostname), q) ||
				strings.Contains(strings.ToLower(d.User), q) ||
				anyTagContains(d.Tags, q)
		}
	}

	seen := make(map[string]struct{})
	ids := make([]string, 0)
	add := func(id string) {
		if _, ok := seen[id]; ok || id == "" {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if poller != nil {
		cache := poller.GetDeviceCache()
		for _, device := range cache.Devices() {
			if match(device) {
				for _, id := range cache.EquivalentIDs(device.ID) {
					add(id)
				}
			}
		}
	}
	if direct != "" && strings.Contains(strings.ToLower(derpRelayName), direct) {
		add(derpRelayIP)
	}
	query.NodeIDs = ids
	query.Match = direct
	return query.Filtered()
}

func anyContains(values []string, term string) bool {
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), term) {
			return true
		}
	}
	return false
}

func anyTagContains(tags []string, term string) bool {
	for _, tag := range tags {
		if strings.Contains(strings.TrimPrefix(strings.ToLower(tag), "tag:"), term) {
			return true
		}
	}
	return false
}

// rankedNode is a stored id resolved through the device cache.
type rankedNode struct {
	id    string
	name  string
	owner string
}

func (h *Handlers) rankedNodeResolver(poller *services.Poller) func(id, storedName string) rankedNode {
	memo := make(map[string]rankedNode)
	return func(id, storedName string) rankedNode {
		if node, ok := memo[id]; ok {
			if node.name == "" {
				node.name = storedName
			}
			return node
		}
		canonical := h.resolveNodeID(poller, id)
		node := rankedNode{
			id:    canonical,
			name:  h.resolveNodeName(poller, canonical),
			owner: h.resolveNodeOwner(poller, canonical),
		}
		memo[id] = node
		if node.name == "" {
			node.name = storedName
		}
		return node
	}
}

// resolveRankedTalkers reports each talker under its canonical device id with
// the device's name and owner, and DERP relay for Tailscale's DERP address.
// Rows for one device stored under several ids (legacy numeric id, address)
// are merged when they land on the same page.
func (h *Handlers) resolveRankedTalkers(poller *services.Poller, talkers []database.RankedTalker, sortBy string) []database.RankedTalker {
	resolve := h.rankedNodeResolver(poller)
	out := make([]database.RankedTalker, 0, len(talkers))
	index := make(map[string]int, len(talkers))
	for _, talker := range talkers {
		node := resolve(talker.NodeID, talker.Hostname)
		if i, ok := index[node.id]; ok {
			out[i].TxBytes += talker.TxBytes
			out[i].RxBytes += talker.RxBytes
			out[i].TotalBytes += talker.TotalBytes
			out[i].FlowCount += talker.FlowCount
			continue
		}
		talker.NodeID, talker.Hostname, talker.Owner = node.id, node.name, node.owner
		index[node.id] = len(out)
		out = append(out, talker)
	}
	if len(out) != len(talkers) {
		sort.SliceStable(out, func(i, j int) bool {
			return rankLess(sortBy, out[i].TotalBytes, out[i].FlowCount, out[j].TotalBytes, out[j].FlowCount)
		})
	}
	return out
}

// resolveRankedPairs does the same for both ends of each directed pair.
func (h *Handlers) resolveRankedPairs(poller *services.Poller, pairs []database.RankedPair, sortBy string) []database.RankedPair {
	resolve := h.rankedNodeResolver(poller)
	out := make([]database.RankedPair, 0, len(pairs))
	index := make(map[[2]string]int, len(pairs))
	for _, pair := range pairs {
		src := resolve(pair.SrcNodeID, pair.SrcHostname)
		dst := resolve(pair.DstNodeID, pair.DstHostname)
		key := [2]string{src.id, dst.id}
		if i, ok := index[key]; ok {
			out[i].TxBytes += pair.TxBytes
			out[i].RxBytes += pair.RxBytes
			out[i].TotalBytes += pair.TotalBytes
			out[i].FlowCount += pair.FlowCount
			continue
		}
		pair.SrcNodeID, pair.SrcHostname, pair.SrcOwner = src.id, src.name, src.owner
		pair.DstNodeID, pair.DstHostname, pair.DstOwner = dst.id, dst.name, dst.owner
		index[key] = len(out)
		out = append(out, pair)
	}
	if len(out) != len(pairs) {
		sort.SliceStable(out, func(i, j int) bool {
			return rankLess(sortBy, out[i].TotalBytes, out[i].FlowCount, out[j].TotalBytes, out[j].FlowCount)
		})
	}
	return out
}

func rankLess(sortBy string, totalA, flowsA, totalB, flowsB int64) bool {
	if sortBy == database.RankSortFlows && flowsA != flowsB {
		return flowsA > flowsB
	}
	return totalA > totalB
}
