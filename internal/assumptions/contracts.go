package assumptions

import "context"

type contractCheckProblem struct {
	kind, code, message string
	differences         []string
}

type contractVerifier struct {
	wire wireVerifier
	http httpContractVerifier
}

// Once captured values agree, check the declarations that make those values
// meaningful. Both CLI viewpoints use this same directional obligation.
func (v *contractVerifier) verify(ctx context.Context, consumer, provider sample) (string, *contractCheckProblem) {
	if consumer.flow.Protocol == "http" {
		return verifyHTTPContracts(ctx, &v.http, consumer, provider)
	}
	if err := v.wire.verify(consumer.flow.Request.GRPC, provider.flow.Request.GRPC, provider.schema, false); err != nil {
		return "", &contractCheckProblem{"mismatch", "provider_wire_mismatch", "consumer request wire is incompatible with the provider contract: " + err.Error(), []string{"protobuf/wire"}}
	}
	if err := v.wire.verify(provider.flow.Response.GRPC, consumer.flow.Response.GRPC, consumer.schema, true); err != nil {
		return "", &contractCheckProblem{"mismatch", "provider_wire_mismatch", "provider response wire is incompatible with the consumer contract: " + err.Error(), []string{"protobuf/wire"}}
	}
	differences, err := v.wire.rulesMatch(consumer, provider)
	if err != nil {
		return "", &contractCheckProblem{"unverified", "protobuf_contract_unavailable", "protobuf declarations cannot be compared reliably: " + err.Error(), []string{"protobuf/contract"}}
	}
	if len(differences) > 0 {
		return "", &contractCheckProblem{"mismatch", "protobuf_contract_mismatch", "consumer and provider protobuf declarations differ for this exercised method, including its reachable message fields and enum values", differences}
	}
	return "protobuf_rules_and_observed_wire", nil
}
