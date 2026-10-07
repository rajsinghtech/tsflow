package database

import (
	"context"
	"time"
)

// ViewerDevice is one device owned by a login, with traffic over a window.
// IDs lists every stored id, including a numeric flow-log id merged onto a
// tagged device. It is not part of the JSON response.
type ViewerDevice struct {
	NodeID     string   `json:"nodeId"`
	Hostname   string   `json:"hostname"`
	Owner      string   `json:"owner"`
	Tags       []string `json:"tags,omitempty"`
	IDs        []string `json:"-"`
	TxBytes    int64    `json:"txBytes"`
	RxBytes    int64    `json:"rxBytes"`
	TotalBytes int64    `json:"totalBytes"`
	FlowCount  int64    `json:"flowCount"`
	Online     bool     `json:"online"`
}

// ListViewerDevices returns every device whose merged login equals login.
// A tagged device is included when its creator login was copied onto it.
// Traffic totals use the same physical-exclusion rules as the other rankings.
func (s *SQLiteStore) ListViewerDevices(ctx context.Context, tailnetID, login string, start, end time.Time, trafficTypes []string) ([]ViewerDevice, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
	identity, err := (IdentityQuery{User: login, ExactUser: true}).normalized()
	if err != nil {
		return nil, err
	}
	if !identity.active() {
		return nil, nil
	}
	devices, err := loadMergedDevices(ctx, s.db, tailnetID)
	if err != nil {
		return nil, err
	}
	matched := matchingDevices(devices, identity)
	talkers, _, err := s.ListRankedTalkers(ctx, tailnetID, start, end, RankQuery{
		Limit:        RankMaxLimit,
		User:         login,
		ExactUser:    true,
		TrafficTypes: trafficTypes,
	})
	if err != nil {
		return nil, err
	}
	byID := map[string]RankedTalker{}
	for _, talker := range talkers {
		byID[talker.NodeID] = talker
	}
	out := make([]ViewerDevice, 0, len(matched))
	for _, device := range matched {
		item := ViewerDevice{
			NodeID:   device.canonical,
			Hostname: device.hostname,
			Owner:    device.owner,
			Tags:     append([]string(nil), device.tags...),
			IDs:      append([]string(nil), device.ids...),
		}
		if item.Hostname == "" {
			item.Hostname = device.name
		}
		if talker, ok := byID[device.canonical]; ok {
			item.TxBytes = talker.TxBytes
			item.RxBytes = talker.RxBytes
			item.TotalBytes = talker.TotalBytes
			item.FlowCount = talker.FlowCount
			if talker.Hostname != "" {
				item.Hostname = talker.Hostname
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// ViewerOwns reports whether nodeID is one of the ids recorded for login.
func (s *SQLiteStore) ViewerOwns(ctx context.Context, tailnetID, login, nodeID string) (bool, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return false, err
	}
	identity, err := (IdentityQuery{User: login, ExactUser: true}).normalized()
	if err != nil {
		return false, err
	}
	devices, err := loadMergedDevices(ctx, s.db, tailnetID)
	if err != nil {
		return false, err
	}
	for _, device := range matchingDevices(devices, identity) {
		for _, id := range device.ids {
			if id == nodeID {
				return true, nil
			}
		}
	}
	return false, nil
}
