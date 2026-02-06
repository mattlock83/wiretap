# Payment Example

This example adds a more realistic multi-service workflow:
- A Payment SUT (`examples/payment/payment-sut`) handles `POST /payments/submit`
- A Transaction service (`examples/payment/transaction-service`) stores and lists transactions
- Both services depend on a fake Account service powered by the Phrond mock server

## Test Scenarios

`examples/js/payment-test.js` covers:
- successful payment submit and transaction lookup
- two failing submits where amount is too high
- one failing submit where the source account has insufficient balance

## Run

From the repo root:

```sh
./examples/run-payment-e2e.sh
```

The runner script will:
- start Phrond core
- start the payment and transaction services
- execute the JS e2e tests
- generate YAML + HTML coverage reports in `./artifacts`
