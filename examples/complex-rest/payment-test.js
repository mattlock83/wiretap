const { test, before, beforeEach, afterEach, after } = require("node:test");
const assert = require("node:assert/strict");
const {
  startRuntime,
  startComplexServices,
  stopComplexServices,
  registerAccounts
} = require("./test-helpers");

const RUN_E2E = Boolean(process.env.PHROND_BASE_URL || process.env.PHROND_HTTP_LISTEN);

let runtime;
let services;

if (!RUN_E2E) {
  test("submits payment and lists transactions", { skip: "set PHROND_BASE_URL to run this example" }, async () => {});
  test("rejects amount too high (6000)", { skip: "set PHROND_BASE_URL to run this example" }, async () => {});
  test("rejects amount too high (15000)", { skip: "set PHROND_BASE_URL to run this example" }, async () => {});
  test("rejects insufficient balance", { skip: "set PHROND_BASE_URL to run this example" }, async () => {});
} else {
  before(async () => {
    runtime = await startRuntime();
    services = await startComplexServices();
  });

  beforeEach(async (t) => {
    const session = await runtime.setupTest({ name: t.name });
    services.dependencyRouter.setTargetBaseUrl(session.mockBaseUrl);
  });

  afterEach(async () => {
    await runtime.teardownTest({ status: "pass" });
  });

  after(async () => {
    const errors = [];
    try {
      await stopComplexServices(services);
    } catch (err) {
      errors.push(err);
    }
    try {
      await runtime?.killServer();
    } catch (err) {
      errors.push(err);
    }
    services = null;
    runtime = null;
    if (errors.length > 0) {
      throw errors[0];
    }
  });

  test("submits payment and lists transactions", async () => {
    const from = { id: "acct-100", name: "Alice", balance: 1000, currency: "USD", status: "active" };
    const to = { id: "acct-200", name: "Bob", balance: 500, currency: "USD", status: "active" };

    await registerAccounts(runtime, [from, to, from]);

    const submit = await runtime.http.postJson("/payments/submit", {
      from_account: from.id,
      to_account: to.id,
      amount: 120,
      currency: "USD"
    });
    assert.equal(submit.status, 201);

    const list = await runtime.http.get(`/transactions?account_id=${from.id}`, {
      sutBaseUrl: `http://${services.transactionService.listen}`
    });
    assert.equal(list.status, 200);
    assert.ok(list.body.transactions.some((tx) => tx.transaction_id === submit.body.transaction_id));

    const paymentLookup = await runtime.getResponse(`account-${from.id}-3`);
    const transactionLookup = await runtime.getResponse(`account-${from.id}-1`);
    assert.equal(paymentLookup.state, "responded");
    assert.equal(transactionLookup.state, "responded");
    assert.equal(paymentLookup.exchange.Request.Method, "GET");
    assert.equal(paymentLookup.exchange.Request.Path, `/accounts/${from.id}`);
    assert.equal(transactionLookup.exchange.Request.Method, "GET");
    assert.equal(transactionLookup.exchange.Request.Path, `/accounts/${from.id}`);
  });

  test("rejects amount too high (6000)", async () => {
    const from = { id: "acct-100", name: "Alice", balance: 12000, currency: "USD", status: "active" };
    const to = { id: "acct-200", name: "Bob", balance: 500, currency: "USD", status: "active" };

    await registerAccounts(runtime, [from, to]);

    await assert.rejects(
      () =>
        runtime.http.postJson("/payments/submit", {
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

  test("rejects amount too high (15000)", async () => {
    const from = { id: "acct-100", name: "Alice", balance: 20000, currency: "USD", status: "active" };
    const to = { id: "acct-200", name: "Bob", balance: 500, currency: "USD", status: "active" };

    await registerAccounts(runtime, [from, to]);

    await assert.rejects(
      () =>
        runtime.http.postJson("/payments/submit", {
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

  test("rejects insufficient balance", async () => {
    const from = { id: "acct-100", name: "Alice", balance: 40, currency: "USD", status: "active" };
    const to = { id: "acct-200", name: "Bob", balance: 500, currency: "USD", status: "active" };

    await registerAccounts(runtime, [from, to]);

    await assert.rejects(
      () =>
        runtime.http.postJson("/payments/submit", {
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
