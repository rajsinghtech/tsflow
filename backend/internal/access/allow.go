package access

import (
	"encoding/json"
	"sort"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
)

// Allow is the union of every matching grant for one request.
// All is set when any match omits tailnets or lists "*".
type Allow struct {
	All bool
	ids map[string]struct{}
}

// Add merges one grant into the allow list.
func (a *Allow) Add(grant config.Grant) {
	if grant.All {
		a.All = true
		return
	}
	for _, id := range grant.Tailnets {
		if a.ids == nil {
			a.ids = make(map[string]struct{})
		}
		a.ids[id] = struct{}{}
	}
}

// Permits reports whether id is visible. A wildcard grant permits every id.
func (a Allow) Permits(id string) bool {
	if a.All {
		return true
	}
	_, ok := a.ids[id]
	return ok
}

// Filter keeps ids the grant allows, in the original order.
func (a Allow) Filter(ids []string) []string {
	if a.All {
		return ids
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if a.Permits(id) {
			out = append(out, id)
		}
	}
	return out
}

// TailnetIDs returns "*" or the sorted explicit ids.
func (a Allow) TailnetIDs() []string {
	if a.All {
		return []string{"*"}
	}
	out := make([]string, 0, len(a.ids))
	for id := range a.ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// grantsFromValues parses capability values.
// An empty list means the capability is present with no tailnet limit.
func grantsFromValues(values []json.RawMessage) (Allow, error) {
	var allow Allow
	if len(values) == 0 {
		allow.All = true
		return allow, nil
	}
	for _, raw := range values {
		grant, err := config.ParseGrant(raw)
		if err != nil {
			return Allow{}, err
		}
		allow.Add(grant)
	}
	return allow, nil
}
