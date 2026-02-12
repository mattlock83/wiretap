const http = require("node:http");
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

function parseListenAddr(listen) {
  const value = String(listen || "").trim();
  const splitAt = value.lastIndexOf(":");
  if (splitAt <= 0 || splitAt >= value.length-1) {
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
  const listen = options.listen || process.env.BASIC_DEPENDENCY_ROUTER_LISTEN || "127.0.0.1:0";
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

module.exports = {
  startBasicRestSUT,
  createDependencyRouter
};
