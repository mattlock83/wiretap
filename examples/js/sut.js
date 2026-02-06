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

async function startHTTPService(options = {}) {
  const {
    name = "service",
    listen,
    binary,
    command,
    env = {},
    healthPath = "/healthz",
    healthTimeoutMs = 12_000
  } = options;

  if (!listen) {
    throw new Error(`${name} listen address is required`);
  }

  const defaultCommand = command || ["go", "run", "./examples/system/sut"];
  const executable = binary || defaultCommand[0];
  const args = binary ? [] : defaultCommand.slice(1);

  const proc = spawn(executable, args, {
    stdio: "inherit",
    env: {
      ...process.env,
      ...env
    }
  });

  try {
    await waitForHealth(`http://${listen}${healthPath}`, healthTimeoutMs);
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

async function startSut(options = {}) {
  const {
    listen = process.env.SUT_LISTEN || "127.0.0.1:18090",
    dependencyBaseUrl,
    binary = process.env.SUT_BINARY
  } = options;

  if (!dependencyBaseUrl) {
    throw new Error("dependencyBaseUrl is required");
  }

  return startHTTPService({
    name: "sut",
    listen,
    binary,
    command: ["go", "run", "./examples/system/sut"],
    env: {
      SUT_LISTEN: listen,
      DEPENDENCY_BASE_URL: dependencyBaseUrl
    }
  });
}

module.exports = {
  startHTTPService,
  startSut,
  stopSut
};
