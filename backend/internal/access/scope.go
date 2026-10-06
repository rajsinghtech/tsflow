package access

import (
	"sort"
	"strings"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
)

// DeviceScope is the default traffic-view filter for one viewer.
// It is not an authorization check. An empty owners and tags list
// matches no devices until the viewer clears it.
type DeviceScope struct {
	Owners []string `json:"owners"`
	Tags   []string `json:"tags"`
}

// DeviceScopeFor builds the optional UI default.
// user matches the viewer login. groups matches tags and owners
// derived from mapping keys the viewer belongs to.
func DeviceScopeFor(autoscope, login string, groups []string, grants map[string]config.Grant) *DeviceScope {
	switch autoscope {
	case config.AccessAutoscopeUser:
		owners := []string{}
		if login = strings.TrimSpace(login); login != "" {
			owners = []string{login}
		}
		return &DeviceScope{Owners: owners, Tags: []string{}}
	case config.AccessAutoscopeGroups:
		return &DeviceScope{
			Owners: groupOwners(groups, grants),
			Tags:   groupTags(groups, grants),
		}
	default:
		return nil
	}
}

func groupOwners(groups []string, grants map[string]config.Grant) []string {
	var owners []string
	seen := map[string]struct{}{}
	for _, group := range mappedGroups(groups, grants) {
		if !strings.Contains(group, "@") {
			continue
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		owners = append(owners, group)
	}
	sort.Strings(owners)
	if owners == nil {
		owners = []string{}
	}
	return owners
}

func groupTags(groups []string, grants map[string]config.Grant) []string {
	var tags []string
	seen := map[string]struct{}{}
	for _, group := range mappedGroups(groups, grants) {
		if strings.Contains(group, "@") {
			continue
		}
		name := strings.TrimPrefix(group, "group:")
		name = strings.TrimPrefix(name, "tag:")
		if name == "" {
			continue
		}
		tag := "tag:" + name
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	if tags == nil {
		tags = []string{}
	}
	return tags
}

func mappedGroups(groups []string, grants map[string]config.Grant) []string {
	if len(grants) == 0 {
		return nil
	}
	var out []string
	seen := map[string]struct{}{}
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		if _, ok := grants[group]; !ok {
			continue
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		out = append(out, group)
	}
	return out
}
