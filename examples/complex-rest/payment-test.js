const { test, before, after } = require("node:test");
const assert = require("node:assert/strict");
const { createRuntime, caseConfig, registerAccounts, assertFinishPass } = require("./test-helpers");

let runtime;

before(async () => {
  runtime = await createRuntime();
});

after(async () => {
  await runtime?.paymentSut?.stop();
  await runtime?.transactionService?.stop();
  await runtime?.suite?.teardown();
});

test("submits payment and lists transactions", async () => {
  const from = { id: "acct-100", name: "Alice", balance: 1000, currency: "USD", status: "active" };
  const to = { id: "acct-200", name: "Bob", balance: 500, currency: "USD", status: "active" };

  const { result, finish } = await runtime.suite.withTest(
    caseConfig("complex-rest-submit-success", runtime.paymentSut.listen),
    async (testCase) => {
      await registerAccounts(testCase, [from, to]);

      const submit = await testCase.postJson("/payments/submit", {
        from_account: from.id,
        to_account: to.id,
        amount: 120,
        currency: "USD"
      });
      assert.equal(submit.status, 201);

      const list = await testCase.get(`/transactions?account_id=${from.id}`, {
        sutBaseUrl: `http://${runtime.transactionService.listen}`
      });
      assert.equal(list.status, 200);
      assert.ok(list.body.transactions.some((tx) => tx.transaction_id === submit.body.transaction_id));

      return { submit };
    }
  );

  assert.ok(result.submit.body.transaction_id.length > 0);
  assertFinishPass(finish);
});

test("rejects amount too high (6000)", async () => {
  const from = { id: "acct-100", name: "Alice", balance: 12000, currency: "USD", status: "active" };
  const to = { id: "acct-200", name: "Bob", balance: 500, currency: "USD", status: "active" };

  const { finish } = await runtime.suite.withTest(
    caseConfig("complex-rest-too-high-6000", runtime.paymentSut.listen),
    async (testCase) => {
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
    }
  );

  assertFinishPass(finish);
});

test("rejects amount too high (15000)", async () => {
  const from = { id: "acct-100", name: "Alice", balance: 20000, currency: "USD", status: "active" };
  const to = { id: "acct-200", name: "Bob", balance: 500, currency: "USD", status: "active" };

  const { finish } = await runtime.suite.withTest(
    caseConfig("complex-rest-too-high-15000", runtime.paymentSut.listen),
    async (testCase) => {
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
    }
  );

  assertFinishPass(finish);
});

test("rejects insufficient balance", async () => {
  const from = { id: "acct-100", name: "Alice", balance: 40, currency: "USD", status: "active" };
  const to = { id: "acct-200", name: "Bob", balance: 500, currency: "USD", status: "active" };

  const { finish } = await runtime.suite.withTest(
    caseConfig("complex-rest-insufficient-balance", runtime.paymentSut.listen),
    async (testCase) => {
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
    }
  );

  assertFinishPass(finish);
});
