const { test, before, after } = require("node:test");
const assert = require("node:assert/strict");
const { setupSuite } = require("../../sdk/js/src/index");
const { startHTTPService } = require("./sut");

const SCHEMAS = [
  {
    id: "payment-sut-openapi",
    role: "sut",
    type: "openapi",
    source: { type: "file", path: "examples/payment/payment-sut-openapi.yaml" }
  },
  {
    id: "transaction-openapi",
    role: "sut",
    type: "openapi",
    source: { type: "file", path: "examples/payment/transaction-openapi.yaml" }
  },
  {
    id: "account-openapi",
    role: "fake",
    type: "openapi",
    source: { type: "file", path: "examples/payment/account-openapi.yaml" }
  }
];

let suite;
let paymentSut;
let transactionService;

function caseConfig(name) {
  return {
    name,
    meta: { suite: "payment-example" },
    sutBaseUrl: `http://${paymentSut.listen}`,
    schemas: SCHEMAS
  };
}

function assertFinishPass(finish) {
  assert.ok(typeof finish.path === "string" && finish.path.length > 0, "artifact path should be returned");
  if (finish.schemaValidation) {
    assert.equal(finish.schemaValidation.status, "pass");
  }
}

async function registerAccounts(testCase, accounts) {
  await testCase.clearMocks();
  for (const account of accounts) {
    await testCase.mockHttp({
      id: `account-${account.id}`,
      match: { method: "GET", path: `/accounts/${account.id}` },
      response: {
        status: 200,
        body: JSON.stringify(account),
        headers: { "Content-Type": "application/json" }
      }
    });
  }
}

before(async () => {
  const baseUrl =
    process.env.PHROND_BASE_URL ||
    (process.env.PHROND_HTTP_LISTEN ? `http://${process.env.PHROND_HTTP_LISTEN}` : "http://127.0.0.1:18000");
  const paymentListen = process.env.PAYMENT_SUT_LISTEN || "127.0.0.1:18110";
  const transactionListen = process.env.TRANSACTION_SERVICE_LISTEN || "127.0.0.1:18111";

  suite = await setupSuite({ baseUrl, proxy: true, mockServer: true });
  const mockAddr = suite.runtime.mockServer?.addr;
  assert.ok(mockAddr, "mock server address must be returned");

  transactionService = await startHTTPService({
    name: "transaction-service",
    listen: transactionListen,
    binary: process.env.TRANSACTION_SERVICE_BINARY,
    command: ["go", "run", "./examples/payment/transaction-service"],
    env: {
      TRANSACTION_SERVICE_LISTEN: transactionListen,
      ACCOUNT_BASE_URL: `http://${mockAddr}`
    }
  });

  paymentSut = await startHTTPService({
    name: "payment-sut",
    listen: paymentListen,
    binary: process.env.PAYMENT_SUT_BINARY,
    command: ["go", "run", "./examples/payment/payment-sut"],
    env: {
      PAYMENT_SUT_LISTEN: paymentListen,
      ACCOUNT_BASE_URL: `http://${mockAddr}`,
      TRANSACTION_BASE_URL: `http://${transactionService.listen}`
    }
  });
});

after(async () => {
  await paymentSut?.stop();
  await transactionService?.stop();
  await suite?.teardown();
});

test("submits payment and fetches transactions from transaction service", async () => {
  const from = { id: "acct-100", name: "Alice", balance: 1000, currency: "USD", status: "active" };
  const to = { id: "acct-200", name: "Bob", balance: 500, currency: "USD", status: "active" };

  const { result, finish } = await suite.withTest(caseConfig("payment-submit-success"), async (testCase) => {
    await registerAccounts(testCase, [from, to]);

    const submit = await testCase.postJson("/payments/submit", {
      from_account: from.id,
      to_account: to.id,
      amount: 120,
      currency: "USD"
    });
    assert.equal(submit.status, 201);
    assert.equal(submit.body.status, "submitted");
    assert.equal(submit.body.from_account, from.id);
    assert.equal(submit.body.to_account, to.id);

    const list = await testCase.get(`/transactions?account_id=${from.id}`, {
      sutBaseUrl: `http://${transactionService.listen}`
    });
    assert.equal(list.status, 200);
    assert.equal(list.body.account.id, from.id);
    assert.ok(Array.isArray(list.body.transactions));
    assert.ok(list.body.transactions.length > 0);
    assert.ok(list.body.transactions.some((tx) => tx.transaction_id === submit.body.transaction_id));
    return { submit, list };
  });

  assert.ok(result.submit.body.transaction_id.length > 0);
  assertFinishPass(finish);
});

test("rejects submit when amount is too high (6000)", async () => {
  const from = { id: "acct-100", name: "Alice", balance: 12000, currency: "USD", status: "active" };
  const to = { id: "acct-200", name: "Bob", balance: 500, currency: "USD", status: "active" };

  const { finish } = await suite.withTest(caseConfig("payment-too-high-6000"), async (testCase) => {
    await registerAccounts(testCase, [from, to]);

    await assert.rejects(
      () =>
        testCase.postJson("/payments/submit", {
          from_account: from.id,
          to_account: to.id,
          amount: 6000,
          currency: "USD"
        }),
      (err) => {
        assert.equal(err.status, 422);
        assert.equal(err.body.error_code, "amount_too_high");
        return true;
      }
    );
  });

  assertFinishPass(finish);
});

test("rejects submit when amount is too high (15000)", async () => {
  const from = { id: "acct-100", name: "Alice", balance: 20000, currency: "USD", status: "active" };
  const to = { id: "acct-200", name: "Bob", balance: 500, currency: "USD", status: "active" };

  const { finish } = await suite.withTest(caseConfig("payment-too-high-15000"), async (testCase) => {
    await registerAccounts(testCase, [from, to]);

    await assert.rejects(
      () =>
        testCase.postJson("/payments/submit", {
          from_account: from.id,
          to_account: to.id,
          amount: 15000,
          currency: "USD"
        }),
      (err) => {
        assert.equal(err.status, 422);
        assert.equal(err.body.error_code, "amount_too_high");
        return true;
      }
    );
  });

  assertFinishPass(finish);
});

test("rejects submit when source account has insufficient balance", async () => {
  const from = { id: "acct-100", name: "Alice", balance: 40, currency: "USD", status: "active" };
  const to = { id: "acct-200", name: "Bob", balance: 500, currency: "USD", status: "active" };

  const { finish } = await suite.withTest(caseConfig("payment-insufficient-balance"), async (testCase) => {
    await registerAccounts(testCase, [from, to]);

    await assert.rejects(
      () =>
        testCase.postJson("/payments/submit", {
          from_account: from.id,
          to_account: to.id,
          amount: 75,
          currency: "USD"
        }),
      (err) => {
        assert.equal(err.status, 422);
        assert.equal(err.body.error_code, "insufficient_balance");
        return true;
      }
    );
  });

  assertFinishPass(finish);
});
