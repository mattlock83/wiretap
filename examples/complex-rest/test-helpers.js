const http = require("node:http");

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

async function registerAccounts(phrond, accounts) {
  for (const [index, account] of accounts.entries()) {
    await phrond.queueResponse({
      id: `account-${account.id}-${index + 1}`,
      assetId: BUILTIN_ACCOUNT_DEPENDENCY.assetId,
      match: { method: "GET", path: `/accounts/${account.id}` },
      response: {
        status: 200,
        json: account
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

function runtimeConfig(options = {}) {
  if (!options.externalAccountUrl) {
    throw new Error("externalAccountUrl is required");
  }

  const baseUrl = resolveBaseUrl();
  const paymentBaseUrl =
    process.env.PAYMENT_SUT_BASE_URL ||
    `http://${process.env.PAYMENT_SUT_LISTEN || "127.0.0.1:18110"}`;
  return {
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
    sutBaseUrl: paymentBaseUrl,
    strictSchema: true,
    meta: { suite: "complex-rest", mockMode: "mixed" }
  };
}

module.exports = {
  BUILTIN_ACCOUNT_DEPENDENCY,
  EXTERNAL_ACCOUNT_DEPENDENCY,
  startExternalAccountMock,
  runtimeConfig,
  registerAccounts
};
