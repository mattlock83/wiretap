const { spawn } = require("node:child_process");
const http = require("node:http");
const { startServer } = require("../../sdk/js/src/index");

const SUT_SCHEMAS = [
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
  }
];

// The transaction service uses Phrond's built-in account mock.
const BUILTIN_ACCOUNT_DEPENDENCY = {
  assetId: "account-service",
  assetName: "Built-in Account Service",
  schema: {
    id: "complex-rest-account-openapi",
    type: "openapi",
    source: { type: "file", path: "examples/complex-rest/schemas/account-openapi.yaml" }
  }
};

// The payment SUT uses a user-owned account mock through Phrond's capture
// gateway. Its upstream URL is supplied after that server starts.
const EXTERNAL_ACCOUNT_DEPENDENCY = {
  assetId: "external-account-service",
  assetName: "External Account Service",
  schema: {
    id: "complex-rest-external-account-openapi",
    type: "openapi",
    source: { type: "file", path: "examples/complex-rest/schemas/account-openapi.yaml" }
  }
};

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function waitForHealth(url, timeoutMs = 12_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      if ((await fetch(url)).ok) return;
    } catch (_) {
      // retry until deadline
    }
    await sleep(25);
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
  for (const [index, account] of accounts.entries()) {
    await phrond.queueResponse({
      id: `account-${account.id}-${index + 1}`,
      assetId: BUILTIN_ACCOUNT_DEPENDENCY.assetId,
      match: { method: "GET", path: `/accounts/${account.id}` },
      response: {
        status: 200,
        body: JSON.stringify(account),
        headers: { "Content-Type": "application/json" }
      }
    });
  }
}

async function startExternalAccountMock() {
  let accountsById = new Map();
  let requests = [];

  const server = http.createServer((req, res) => {
    const url = new URL(req.url, "http://external-account-mock");
    const match = /^\/accounts\/([^/]+)$/.exec(url.pathname);

    requests.push({ method: req.method, path: url.pathname, query: url.search });

    if (req.method !== "GET" || !match) {
      res.writeHead(404, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ error: "not found" }));
      return;
    }

    const account = accountsById.get(decodeURIComponent(match[1]));
    if (!account) {
      res.writeHead(404, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ error: "account not found" }));
      return;
    }

    res.writeHead(200, {
      "Content-Type": "application/json",
      "X-Mock-Provider": "example-byo-mock"
    });
    res.end(JSON.stringify(account));
  });

  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });

  const address = server.address();
  return {
    baseUrl: `http://127.0.0.1:${address.port}`,
    reset(accounts) {
      accountsById = new Map(accounts.map((account) => [account.id, account]));
      requests = [];
    },
    requests() {
      return requests.map((request) => ({ ...request }));
    },
    stop() {
      return new Promise((resolve, reject) => {
        server.close((err) => (err ? reject(err) : resolve()));
      });
    }
  };
}

function resolveBaseUrl() {
  return (
    process.env.PHROND_BASE_URL ||
    (process.env.PHROND_HTTP_LISTEN ? `http://${process.env.PHROND_HTTP_LISTEN}` : "http://127.0.0.1:18000")
  );
}

async function startRuntime(options = {}) {
  if (!options.externalAccountUrl) {
    throw new Error("externalAccountUrl is required");
  }

  const baseUrl = resolveBaseUrl();
  const paymentListen = process.env.PAYMENT_SUT_LISTEN || "127.0.0.1:18110";
  return startServer({
    baseUrl,
    proxy: process.env.PHROND_PROXY_LISTEN ? { listenAddr: process.env.PHROND_PROXY_LISTEN } : true,
    mockServer: process.env.PHROND_MOCK_LISTEN ? { listenAddr: process.env.PHROND_MOCK_LISTEN } : true,
    schemas: SUT_SCHEMAS,
    dependencies: [
      BUILTIN_ACCOUNT_DEPENDENCY,
      {
        ...EXTERNAL_ACCOUNT_DEPENDENCY,
        mock: {
          mode: "external",
          upstreamUrl: options.externalAccountUrl
        }
      }
    ],
    sutBaseUrl: `http://${paymentListen}`,
    strictSchema: true,
    meta: { suite: "complex-rest", mockMode: "mixed" }
  });
}

async function startComplexServices(runtime, options = {}) {
  const paymentListen = options.paymentListen || process.env.PAYMENT_SUT_LISTEN || "127.0.0.1:18110";
  const transactionListen = options.transactionListen || process.env.TRANSACTION_SERVICE_LISTEN || "127.0.0.1:18111";

  // Both URLs are stable across tests. Phrond serves the first dependency and
  // gateways the second to the user-owned mock while recording both.
  const builtinAccountBaseUrl = runtime.dependencyUrl(BUILTIN_ACCOUNT_DEPENDENCY.assetId);
  const externalAccountBaseUrl = runtime.dependencyUrl(EXTERNAL_ACCOUNT_DEPENDENCY.assetId);

  let transactionService;
  let paymentSut;

  try {
    transactionService = await startService({
      listen: transactionListen,
      binary: process.env.TRANSACTION_SERVICE_BINARY,
      command: ["go", "run", "./examples/demo-apps/complex-rest/transaction-service"],
      env: {
        TRANSACTION_SERVICE_LISTEN: transactionListen,
        ACCOUNT_BASE_URL: builtinAccountBaseUrl
      }
    });

    paymentSut = await startService({
      listen: paymentListen,
      binary: process.env.PAYMENT_SUT_BINARY,
      command: ["go", "run", "./examples/demo-apps/complex-rest/payment-sut"],
      env: {
        PAYMENT_SUT_LISTEN: paymentListen,
        ACCOUNT_BASE_URL: externalAccountBaseUrl,
        TRANSACTION_BASE_URL: `http://${transactionService.listen}`
      }
    });
  } catch (err) {
    await paymentSut?.stop?.();
    await transactionService?.stop?.();
    throw err;
  }

  return {
    paymentSut,
    transactionService
  };
}

async function stopComplexServices(services) {
  const errors = [];
  try {
    await services?.paymentSut?.stop?.();
  } catch (err) {
    errors.push(err);
  }
  try {
    await services?.transactionService?.stop?.();
  } catch (err) {
    errors.push(err);
  }
  if (errors.length > 0) {
    throw errors[0];
  }
}

module.exports = {
  BUILTIN_ACCOUNT_DEPENDENCY,
  EXTERNAL_ACCOUNT_DEPENDENCY,
  startExternalAccountMock,
  startRuntime,
  startComplexServices,
  stopComplexServices,
  registerAccounts
};
