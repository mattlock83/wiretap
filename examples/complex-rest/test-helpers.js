const http = require("node:http");
const { spawn } = require("node:child_process");
const { startServer } = require("../../sdk/js/src/index");

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

function parseListenAddr(listen) {
  const value = String(listen || "").trim();
  const splitAt = value.lastIndexOf(":");
  if (splitAt <= 0 || splitAt >= value.length - 1) {
    throw new Error(`invalid listen address: ${listen}`);
  }
  const host = value.slice(0, splitAt);
  const port = Number(value.slice(splitAt + 1));
  if (!Number.isInteger(port) || port < 0 || port > 65535) {
    throw new Error(`invalid listen port: ${listen}`);
  }
  return { host, port };
}

function joinBaseAndRequestPath(basePath, requestPath) {
  const prefix = basePath === "/" ? "" : basePath.replace(/\/$/, "");
  const suffix = requestPath.startsWith("/") ? requestPath : `/${requestPath}`;
  return prefix + suffix || "/";
}

function buildForwardURL(baseUrl, incomingPath) {
  const base = new URL(baseUrl);
  const incoming = new URL(incomingPath || "/", "http://placeholder.local");
  base.pathname = joinBaseAndRequestPath(base.pathname || "/", incoming.pathname || "/");
  base.search = incoming.search;
  return base;
}

async function createDependencyRouter(options = {}) {
  const listen = options.listen || process.env.COMPLEX_DEPENDENCY_ROUTER_LISTEN || "127.0.0.1:0";
  const { host, port } = parseListenAddr(listen);
  let targetBaseUrl = "";

  const server = http.createServer(async (req, res) => {
    if (!targetBaseUrl) {
      res.statusCode = 503;
      res.end("dependency target is not set");
      return;
    }

    try {
      const upstreamURL = buildForwardURL(targetBaseUrl, req.url || "/");
      const chunks = [];
      for await (const chunk of req) {
        chunks.push(chunk);
      }
      const body = chunks.length > 0 ? Buffer.concat(chunks) : undefined;

      const headers = { ...req.headers };
      delete headers.host;

      const upstream = await fetch(upstreamURL, {
        method: req.method || "GET",
        headers,
        body
      });

      res.statusCode = upstream.status;
      upstream.headers.forEach((value, key) => {
        if (key.toLowerCase() === "transfer-encoding") {
          return;
        }
        res.setHeader(key, value);
      });
      const responseBody = Buffer.from(await upstream.arrayBuffer());
      res.end(responseBody);
    } catch (err) {
      res.statusCode = 502;
      res.end(`dependency forward error: ${err.message}`);
    }
  });

  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(port, host, resolve);
  });

  const address = server.address();
  if (!address || typeof address === "string") {
    throw new Error("failed to resolve dependency router listen address");
  }

  const resolvedListen = `${address.address}:${address.port}`;
  return {
    listen: resolvedListen,
    baseUrl: `http://${resolvedListen}`,
    setTargetBaseUrl(value) {
      if (typeof value !== "string" || value.trim() === "") {
        throw new Error("dependency target base URL is required");
      }
      targetBaseUrl = value.trim().replace(/\/$/, "");
    },
    stop() {
      return new Promise((resolve) => server.close(resolve));
    }
  };
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
      match: { method: "GET", path: `/accounts/${account.id}` },
      response: {
        status: 200,
        body: JSON.stringify(account),
        headers: { "Content-Type": "application/json" }
      }
    });
  }
}

async function registerTemplateLinkedAccounts(phrond, options = {}) {
  const fromAccountID = options.fromAccountId || "acct-template-from";
  const toAccountID = options.toAccountId || "acct-template-to";

  await phrond.queueResponse({
    id: `account-template-context-${fromAccountID}`,
    match: { method: "GET", path: `/accounts/${fromAccountID}` },
    response: {
      status: 200,
      bodyFile: "examples/complex-rest/templates/account-context-shared.tmpl.json",
      headers: { "Content-Type": "application/json" }
    }
  });

  await phrond.queueResponse({
    id: `account-template-dependent-${toAccountID}`,
    match: { method: "GET", path: `/accounts/${toAccountID}` },
    response: {
      status: 200,
      bodyFile: "examples/complex-rest/templates/account-dependent-shared.tmpl.json",
      headers: { "Content-Type": "application/json" }
    }
  });

  return {
    fromAccountID,
    toAccountID
  };
}

function resolveBaseUrl() {
  return (
    process.env.PHROND_BASE_URL ||
    (process.env.PHROND_HTTP_LISTEN ? `http://${process.env.PHROND_HTTP_LISTEN}` : "http://127.0.0.1:18000")
  );
}

async function startRuntime() {
  const baseUrl = resolveBaseUrl();
  const paymentListen = process.env.PAYMENT_SUT_LISTEN || "127.0.0.1:18110";
  return startServer({
    baseUrl,
    proxy: process.env.PHROND_PROXY_LISTEN ? { listenAddr: process.env.PHROND_PROXY_LISTEN } : true,
    mockServer: process.env.PHROND_MOCK_LISTEN ? { listenAddr: process.env.PHROND_MOCK_LISTEN } : true,
    schemas: SCHEMAS,
    sutBaseUrl: `http://${paymentListen}`,
    strictSchema: true,
    meta: { suite: "complex-rest" }
  });
}

async function startComplexServices(options = {}) {
  const paymentListen = options.paymentListen || process.env.PAYMENT_SUT_LISTEN || "127.0.0.1:18110";
  const transactionListen = options.transactionListen || process.env.TRANSACTION_SERVICE_LISTEN || "127.0.0.1:18111";
  const dependencyRouter = await createDependencyRouter({
    listen: options.dependencyRouterListen || process.env.COMPLEX_DEPENDENCY_ROUTER_LISTEN || "127.0.0.1:0"
  });

  let transactionService;
  let paymentSut;

  try {
    transactionService = await startService({
      listen: transactionListen,
      binary: process.env.TRANSACTION_SERVICE_BINARY,
      command: ["go", "run", "./examples/demo-apps/complex-rest/transaction-service"],
      env: {
        TRANSACTION_SERVICE_LISTEN: transactionListen,
        ACCOUNT_BASE_URL: dependencyRouter.baseUrl
      }
    });

    paymentSut = await startService({
      listen: paymentListen,
      binary: process.env.PAYMENT_SUT_BINARY,
      command: ["go", "run", "./examples/demo-apps/complex-rest/payment-sut"],
      env: {
        PAYMENT_SUT_LISTEN: paymentListen,
        ACCOUNT_BASE_URL: dependencyRouter.baseUrl,
        TRANSACTION_BASE_URL: `http://${transactionService.listen}`
      }
    });
  } catch (err) {
    await paymentSut?.stop?.();
    await transactionService?.stop?.();
    await dependencyRouter.stop();
    throw err;
  }

  return {
    paymentSut,
    transactionService,
    dependencyRouter
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
  try {
    await services?.dependencyRouter?.stop?.();
  } catch (err) {
    errors.push(err);
  }
  if (errors.length > 0) {
    throw errors[0];
  }
}

module.exports = {
  startRuntime,
  startComplexServices,
  stopComplexServices,
  registerAccounts,
  registerTemplateLinkedAccounts
};
