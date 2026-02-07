# Payment gRPC + REST Mixed Example

This example mixes protocols in one test run:
- Payment SUT is gRPC (`examples/paymentgrpc/sut`)
- Account dependency is gRPC and faked via Phrond gRPC mock
- Transaction service is REST (`examples/paymentgrpc/transaction-service`)
- Payment contract is defined in `examples/paymentgrpc/paymentpb/payment.proto`
- Account contract is defined in `examples/paymentgrpc/accountpb/account.proto`

The test client (`examples/paymentgrpc/client`) runs four cases:
- successful payment submit plus transaction list check
- payment rejected when amount is too high (6000)
- payment rejected when amount is too high (15000)
- payment rejected when source account has insufficient balance

The generated artifact and coverage report include both gRPC and HTTP traffic.

## Run

From the repo root:

```bash
./examples/run-payment-grpc-mixed-e2e.sh
```
