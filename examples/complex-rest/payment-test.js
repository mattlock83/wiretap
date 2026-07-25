const { test, before, after } = require("node:test");
const assert = require("node:assert/strict");
const { createNodeTestSuite } = require("../../sdk/js/src/index");
const {
  startExternalAccountMock,
  runtimeConfig,
  registerAccounts
} = require("./test-helpers");

const RUN_E2E = Boolean(process.env.PHROND_BASE_URL || process.env.PHROND_HTTP_LISTEN);

let externalAccountMock;

if (!RUN_E2E) {
  test("submits payment and lists transactions", { skip: "set PHROND_BASE_URL to run this example" }, async () => {});
  test("rejects amount too high (6000)", { skip: "set PHROND_BASE_URL to run this example" }, async () => {});
  test("rejects amount too high (15000)", { skip: "set PHROND_BASE_URL to run this example" }, async () => {});
  test("rejects insufficient balance", { skip: "set PHROND_BASE_URL to run this example" }, async () => {});
} else {
  // The runner owns the payment and transaction service processes and their
  // configuration. The suite starts only Phrond's capture components.
  const phrond = createNodeTestSuite({
    runtimeConfig: () => runtimeConfig({ externalAccountUrl: externalAccountMock.baseUrl }),
    autoHooks: false
  });

  before(async () => {
    externalAccountMock = await startExternalAccountMock();
    await phrond.start();
  });

  after(async () => {
    try {
      await phrond.close();
    } finally {
      await externalAccountMock?.stop?.();
      externalAccountMock = null;
    }
  });

  phrond.test("submits payment and lists transactions", async () => {
    const from = { id: "acct-100", name: "Alice", balance: 1000, currency: "USD", status: "active" };
    const to = { id: "acct-200", name: "Bob", balance: 500, currency: "USD", status: "active" };

    externalAccountMock.reset([from, to]);
    await registerAccounts(phrond, [from]);

    const submit = await phrond.http.postJson("/payments/submit", {
      from_account: from.id,
      to_account: to.id,
      amount: 120,
      currency: "USD"
    });
    assert.equal(submit.status, 201);

    const list = await phrond.http.get(`/transactions?account_id=${from.id}`, {
      sutBaseUrl:
        process.env.TRANSACTION_SERVICE_BASE_URL ||
        `http://${process.env.TRANSACTION_SERVICE_LISTEN || "127.0.0.1:18111"}`
    });
    assert.equal(list.status, 200);
    assert.ok(list.body.transactions.some((tx) => tx.transaction_id === submit.body.transaction_id));

    const transactionLookup = await phrond.getResponse(`account-${from.id}-1`);
    assert.equal(transactionLookup.state, "responded");
    assert.equal(transactionLookup.exchange.Request.Method, "GET");
    assert.equal(transactionLookup.exchange.Request.Path, `/accounts/${from.id}`);

    assert.deepEqual(externalAccountMock.requests(), [
      { method: "GET", path: `/accounts/${from.id}`, query: "" },
      { method: "GET", path: `/accounts/${to.id}`, query: "" }
    ]);

    // Built-in verification remains available alongside the external mock.
    await phrond.verify(`account-${from.id}-1`, ["body.id", "body.status"]);
  });

  phrond.test("rejects amount too high (6000)", async () => {
    const from = { id: "acct-100", name: "Alice", balance: 12000, currency: "USD", status: "active" };
    const to = { id: "acct-200", name: "Bob", balance: 500, currency: "USD", status: "active" };

    externalAccountMock.reset([from, to]);

    await assert.rejects(
      () =>
        phrond.http.postJson("/payments/submit", {
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

  phrond.test("rejects amount too high (15000)", async () => {
    const from = { id: "acct-100", name: "Alice", balance: 20000, currency: "USD", status: "active" };
    const to = { id: "acct-200", name: "Bob", balance: 500, currency: "USD", status: "active" };

    externalAccountMock.reset([from, to]);

    await assert.rejects(
      () =>
        phrond.http.postJson("/payments/submit", {
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

  phrond.test("rejects insufficient balance", async () => {
    const from = { id: "acct-100", name: "Alice", balance: 40, currency: "USD", status: "active" };
    const to = { id: "acct-200", name: "Bob", balance: 500, currency: "USD", status: "active" };

    externalAccountMock.reset([from, to]);

    await assert.rejects(
      () =>
        phrond.http.postJson("/payments/submit", {
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
}
