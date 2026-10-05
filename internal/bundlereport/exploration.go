package bundlereport

import (
	"strings"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
)

// One parent test can invoke exploration repeatedly with different scopes or
// fixtures. Keep those invocations distinct even though their source IDs match.
func explorationGroupKey(x *artifact.Exploration) string {
	return strings.Join([]string{x.SourceKind, x.SourceRunID, x.SourceAttemptID, x.InvocationID, x.SourceSHA256}, "|")
}

func explorationParentCommitted(run *bundle.Run, x *artifact.Exploration) bool {
	if x.SourceKind != "active_test" || x.SourceRunID != run.RunID {
		return false
	}
	for _, attempt := range run.Attempts {
		if attempt.AttemptID == x.SourceAttemptID {
			return true
		}
	}
	return false
}

const explorationScope = "Generated attempts retain fresh provider exchanges and any downstream traffic actually observed during that case. Fixture and inspection traffic from lifecycle callbacks is excluded from evidence and coverage. Missing dependency traffic does not establish that the system has no dependencies. Hook code, credentials and runtime fixture context stay in the test project."
