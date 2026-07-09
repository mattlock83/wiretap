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

// The SUT is configured once with a stable dependency base URL — no per-test
// rewiring and no forwarding shim required.
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

module.exports = {
  startBasicRestSUT
};
