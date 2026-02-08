const { test, before, beforeEach, afterEach, after } = require("node:test");
const assert = require("node:assert/strict");
const { createRuntime, registerAccounts } = require("./test-helpers");

let runtime;

before(async () => {
  runtime = await createRuntime();
});

beforeEach(async (t) => {
  await runtime.reset();
  await runtime.scenarioName(t.name);
});

afterEach(async () => {
  await runtime.mock.clear();
});

after(async () => {
  await runtime?.stop();
  await runtime?.paymentSut?.stop();
  await runtime?.transactionService?.stop();
});

test("submits payment and lists transactions", async () => {
  const from = { id: "acct-100", name: "Alice", balance: 1000, currency: "USD", status: "active" };
  const to = { id: "acct-200", name: "Bob", balance: 500, currency: "USD", status: "active" };

  await registerAccounts(runtime, [from, to]);

  const submit = await runtime.http.postJson("/payments/submit", {
    from_account: from.id,
    to_account: to.id,
    amount: 120,
    currency: "USD"
  });
  assert.equal(submit.status, 201);

  const list = await runtime.http.get(`/transactions?account_id=${from.id}`, {
    sutBaseUrl: `http://${runtime.transactionService.listen}`
  });
  assert.equal(list.status, 200);
  assert.ok(list.body.transactions.some((tx) => tx.transaction_id === submit.body.transaction_id));

  const fromAccountLookup = await runtime.expectCall({ method: "GET", path: `/accounts/${from.id}` });
  assert.equal(fromAccountLookup.method, "GET");
  assert.equal(fromAccountLookup.path, `/accounts/${from.id}`);
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
