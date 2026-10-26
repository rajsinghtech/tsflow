package mcpserver

import (
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// Allowed values for the enumerated tool arguments. The descriptions already
// name them; the schema enums let clients offer or check them directly.
var (
	scopeEnum       = []any{scopeMine, scopeAll}
	sortEnum        = []any{"bytes", "flows"}
	trafficTypeEnum = []any{"virtual", "subnet", "exit", "physical"}
)

// Built once: serverFor runs per request, and the SDK caches resolved
// schemas by pointer, so a fresh schema per request would never hit the cache.
var (
	deviceInSchema         = inputSchema[deviceIn]()
	flowsBetweenInSchema   = inputSchema[flowsBetweenIn]()
	getDeviceInSchema      = inputSchema[getDeviceIn]()
	newConnectionsInSchema = inputSchema[newConnectionsIn]()
	rankedInSchema         = inputSchema[rankedIn]()
	searchDevicesInSchema  = inputSchema[searchDevicesIn]()
	statsOverviewInSchema  = inputSchema[statsOverviewIn]()
	timelineInSchema       = inputSchema[timelineIn]()
)

// inputSchema infers T's schema the way the SDK does and adds enums for the
// scope, sort, and trafficTypes arguments when T has them.
func inputSchema[T any]() *jsonschema.Schema {
	s, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("mcp input schema: %v", err))
	}
	if p := s.Properties["scope"]; p != nil {
		p.Enum = scopeEnum
	}
	if p := s.Properties["sort"]; p != nil {
		p.Enum = sortEnum
	}
	if p := s.Properties["trafficTypes"]; p != nil && p.Items != nil {
		p.Items.Enum = trafficTypeEnum
	}
	return s
}
