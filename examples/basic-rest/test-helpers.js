const { spawn } = require("node:child_process");
const { start } = require("../../sdk/js/src/index");

const SCHEMAS = [
  {
    id: "basic-rest-sut-openapi",
    role: "sut",
    type: "openapi",
    source: { type: "file", path: "examples/basic-rest/schemas/sut-openapi.yaml" }
  },
  {
    id: "basic-rest-fake-openapi",
    role: "fake",
    type: "openapi",
    source: { type: "file", path: "examples/basic-rest/schemas/fake-openapi.yaml" }
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

async function startBasicRestSUT(options = {}) {
  const listen = options.listen || process.env.SUT_LISTEN || "127.0.0.1:18090";
  const dependencyBaseUrl = options.dependencyBaseUrl;
  const binary = options.binary || process.env.SUT_BINARY;

  if (!dependencyBaseUrl) {
    throw new Error("dependencyBaseUrl is required");
  }

  const command = binary ? [binary] : ["go", "run", "./examples/demo-apps/basic-rest/sut"];
  const proc = spawn(command[0], command.slice(1), {
    stdio: "inherit",
    env: {
      ...process.env,
      SUT_LISTEN: listen,
      DEPENDENCY_BASE_URL: dependencyBaseUrl
    }
  });

  try {
    await waitForHealth(`http://${listen}/healthz`);
  } catch (err) {
    await stopProcess(proc);
    throw err;
  }

  return {
    listen,
    stop: () => stopProcess(proc)
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
  const sutListen = process.env.SUT_LISTEN || "127.0.0.1:18090";

  return start({
    baseUrl,
    proxy: process.env.PHROND_PROXY_LISTEN ? { listenAddr: process.env.PHROND_PROXY_LISTEN } : true,
    mockServer: process.env.PHROND_MOCK_LISTEN ? { listenAddr: process.env.PHROND_MOCK_LISTEN } : true,
    schemas: SCHEMAS,
    sutBaseUrl: `http://${sutListen}`,
    strictSchema: true
  });
}

async function setupBasicCase(runtime, options = {}) {
  const sutListen = options.sutListen || process.env.SUT_LISTEN || "127.0.0.1:18090";
  const session = await runtime.setup({ name: options.name });

  const sut = await startBasicRestSUT({
    listen: sutListen,
    dependencyBaseUrl: session.mockBaseUrl
  });

  return {
    session,
    sut
  };
}

async function resetBasicCase(runtime, sut, options = {}) {
  const errors = [];
  try {
    await runtime.reset({ status: options.status || "pass" });
  } catch (err) {
    errors.push(err);
  }
  try {
    await sut?.stop?.();
  } catch (err) {
    errors.push(err);
  }
  if (errors.length > 0) {
    throw errors[0];
  }
}

module.exports = {
  startRuntime,
  setupBasicCase,
  resetBasicCase
};
