package assumptions

import "context"

type contractCheckProblem struct {
	kind, code, message string
	differences         []string
}

type contractVerifier struct {
	http httpContractVerifier
}

// Once captured values agree, check the declarations that make those values
// meaningful. Both CLI viewpoints use this same directional obligation.
func (v *contractVerifier) verify(ctx context.Context, consumer, provider sample) (string, *contractCheckProblem) {
	return verifyHTTPContracts(ctx, &v.http, consumer, provider)
}
