package tsnetserve

import (
	"testing"

	"tailscale.com/feature"
)

// TestWorkloadIdentityHookRegistered proves the tsnet join path can mint an
// auth key from a workload identity client. identityfederation's init calls
// feature.Register and then sets HookResolveAuthKeyViaWIF. Register is the
// public signal that the hook was set. This test does not import the feature
// itself. The production package does.
func TestWorkloadIdentityHookRegistered(t *testing.T) {
	if !feature.IsRegistered("identityfederation") {
		t.Fatal("identityfederation is not registered; tsnet WIF join would ignore TS_CLIENT_ID")
	}
}
