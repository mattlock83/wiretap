const { spawn } = require("node:child_process");
const assert = require("node:assert/strict");
const { start } = require("../../sdk/js/src/index");

const SCHEMAS = [
  {
    id: "complex-rest-payment-openapi",
    role: "sut",
    type: "openapi",
    source: { type: "file", path: "examples/complex-rest/schemas/payment-sut-openapi.yaml" }
  },
  {
    id: "complex-rest-transaction-openapi",
    role: "sut",
    type: "openapi",
    source: { type: "file", path: "examples/complex-rest/schemas/transaction-openapi.yaml" }
  },
  {
    id: "complex-rest-account-openapi",
    role: "fake",
    type: "openapi",
    source: { type: "file", path: "examples/complex-rest/schemas/account-openapi.yaml" }
  }
];

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function waitForHealth(url, timeoutMs = 12_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      if ((await fetch(url)).ok) return;
    } catch (_) {
      // retry until deadline
    }
    await sleep(200);
  }
  throw new Error(`service did not become healthy: ${url}`);
}

async function stopProcess(proc, timeoutMs = 2_000) {
  if (!proc || proc.exitCode !== null) return;

  proc.kill("SIGTERM");
  await Promise.race([
    new Promise((resolve) => proc.once("exit", resolve)),
    sleep(timeoutMs).then(() => {
      if (proc.exitCode === null) proc.kill("SIGKILL");
    })
  ]);
}

async function startService(options = {}) {
  const {
    listen,
    binary,
    command,
    env = {},
    healthPath = "/healthz",
    healthTimeoutMs = 12_000
  } = options;

  if (!listen) {
    throw new Error("listen address is required");
  }

  const cmd = binary ? [binary] : command;
  const proc = spawn(cmd[0], cmd.slice(1), {
    stdio: "inherit",
    env: {
      ...process.env,
      ...env
    }
  });

  try {
    await waitForHealth(`http://${listen}${healthPath}`, healthTimeoutMs);
  } catch (err) {
    await stopProcess(proc);
    throw err;
  }

  return {
    listen,
    stop: () => stopProcess(proc)
  };
}

async function registerAccounts(phrond, accounts) {
  for (const account of accounts) {
    await phrond.mock.http({
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

async function createRuntime() {
  const baseUrl =
    process.env.PHROND_BASE_URL ||
    (process.env.PHROND_HTTP_LISTEN ? `http://${process.env.PHROND_HTTP_LISTEN}` : "http://127.0.0.1:18000");
  const paymentListen = process.env.PAYMENT_SUT_LISTEN || "127.0.0.1:18110";
  const transactionListen = process.env.TRANSACTION_SERVICE_LISTEN || "127.0.0.1:18111";

  const session = await start({
    baseUrl,
    proxy: true,
    mockServer: true,
    schemas: SCHEMAS,
    sutBaseUrl: `http://${paymentListen}`,
    strictSchema: true,
    meta: { suite: "complex-rest" }
  });

  const mockAddr = session.runtime.mockServer?.addr;
  assert.ok(mockAddr, "mock server address must be returned");

  const transactionService = await startService({
    listen: transactionListen,
    binary: process.env.TRANSACTION_SERVICE_BINARY,
    command: ["go", "run", "./examples/demo-apps/complex-rest/transaction-service"],
    env: {
      TRANSACTION_SERVICE_LISTEN: transactionListen,
      ACCOUNT_BASE_URL: `http://${mockAddr}`
    }
  });

  const paymentSut = await startService({
    listen: paymentListen,
    binary: process.env.PAYMENT_SUT_BINARY,
    command: ["go", "run", "./examples/demo-apps/complex-rest/payment-sut"],
    env: {
      PAYMENT_SUT_LISTEN: paymentListen,
      ACCOUNT_BASE_URL: `http://${mockAddr}`,
      TRANSACTION_BASE_URL: `http://${transactionService.listen}`
    }
  });

  return {
    runtime: session.runtime,
    mock: session.mock,
    http: session.http,
    scenarioName: session.scenarioName.bind(session),
    expectCall: session.expectCall.bind(session),
    reset: session.reset.bind(session),
    stop: session.stop.bind(session),
    paymentSut,
    transactionService
  };
}

module.exports = {
  createRuntime,
  registerAccounts
};
