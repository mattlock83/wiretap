const { spawn } = require("node:child_process");

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
  throw new Error(`SUT did not become healthy: ${url}`);
}

async function stopSut(proc, timeoutMs = 2_000) {
  if (!proc || proc.exitCode !== null) return;

  proc.kill("SIGTERM");
  await Promise.race([
    new Promise((resolve) => proc.once("exit", resolve)),
    sleep(timeoutMs).then(() => {
      if (proc.exitCode === null) proc.kill("SIGKILL");
    })
  ]);
}

async function startSut(options = {}) {
  const {
    listen = process.env.SUT_LISTEN || "127.0.0.1:18090",
    dependencyBaseUrl,
    binary = process.env.SUT_BINARY,
    healthTimeoutMs = 12_000
  } = options;

  if (!dependencyBaseUrl) {
    throw new Error("dependencyBaseUrl is required");
  }

  const proc = spawn(binary || "go", binary ? [] : ["run", "./examples/system/sut"], {
    stdio: "inherit",
    env: {
      ...process.env,
      SUT_LISTEN: listen,
      DEPENDENCY_BASE_URL: dependencyBaseUrl
    }
  });

  try {
    await waitForHealth(`http://${listen}/healthz`, healthTimeoutMs);
  } catch (err) {
    await stopSut(proc);
    throw err;
  }

  return {
    process: proc,
    listen,
    stop: () => stopSut(proc)
  };
}

module.exports = {
  startSut,
  stopSut
};
