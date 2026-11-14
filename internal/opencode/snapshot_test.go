package opencode_test

import (
	"testing"
	"time"

	"github.com/berry-shake/cpa-opencode-plugin/internal/opencode"
	"github.com/berry-shake/cpa-provider-core/core"
	"github.com/berry-shake/cpa-provider-core/coretest"
)

// TestSnapshots replays testdata/snapshots through the plugin (see
// cpa-provider-core's coretest package and README).
//
//	go test ./internal/opencode -run TestSnapshots           # compare
//	go test ./internal/opencode -run TestSnapshots -update   # rewrite outputs
func TestSnapshots(t *testing.T) {
	coretest.RunSnapshots(t, coretest.Binding{
		ProviderID: opencode.ProviderID,
		NewService: func(version string, host func(string, []byte) ([]byte, error), upstreamURL string) coretest.Plugin {
			return core.NewWithOptions(opencode.New(), version, host, core.Options{
				Now:             func() time.Time { return coretest.Clock },
				ManualRefresh:   true,
				EgressCheckURLs: []string{upstreamURL + "/json"},
			})
		},
		Cases: func(t *testing.T) []coretest.Fixture {
			var out []coretest.Fixture
			for _, c := range snapshotCases() {
				out = append(out, c.fixture(t))
			}
			return out
		},
	})
}

// The case table (snapshot_cases_test.go) predates coretest; these names
// keep it as it was written.
type (
	snapshotFixture     = coretest.Fixture
	snapshotSetup       = coretest.Setup
	snapshotCredential  = coretest.Credential
	snapshotStep        = coretest.Step
	snapshotRPC         = coretest.RPC
	snapshotReply       = coretest.Reply
	cpaLifecycleRequest = coretest.LifecycleRequest
	cpaAuthModelRequest = coretest.AuthModelRequest
)

const snapshotAuthDir = coretest.AuthDir

var caseJSON = coretest.CaseJSON
