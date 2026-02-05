const { createHarness } = require("../../sdk/js/src/index");
const { spawn } = require("node:child_process");
const http2 = require("node:http2");
const net = require("node:net");

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function waitForPort(host, port, retries = 30) {
  for (let i = 0; i < retries; i += 1) {
    try {
      await new Promise((resolve, reject) => {
        const socket = net.createConnection({ host, port }, () => {
          socket.end();
          resolve();
        });
        socket.on("error", reject);
      });
      return true;
    } catch (_) {
      await sleep(200);
    }
  }
  return false;
}

function encodeGrpcFrame(buffer) {
  const header = Buffer.alloc(5);
  header.writeUInt8(0, 0);
  header.writeUInt32BE(buffer.length, 1);
  return Buffer.concat([header, buffer]);
}

function decodeGrpcFrames(buffer) {
  const messages = [];
  let offset = 0;
  while (offset + 5 <= buffer.length) {
    const length = buffer.readUInt32BE(offset + 1);
    offset += 5;
    if (offset + length > buffer.length) {
      break;
    }
    messages.push(buffer.slice(offset, offset + length));
    offset += length;
  }
  return messages;
}

async function callGrpc(addr, path, message) {
  return new Promise((resolve, reject) => {
    const client = http2.connect(`https://${addr}`, {
      rejectUnauthorized: false
    });

    const req = client.request({
      ":method": "POST",
      ":path": path,
      "content-type": "application/grpc",
      "te": "trailers"
    });

    const chunks = [];
    let trailers = {};

    req.on("response", () => {
      // no-op
    });

    req.on("data", (chunk) => chunks.push(chunk));
    req.on("trailers", (headers) => {
      trailers = headers;
    });
    req.on("end", () => {
      const body = Buffer.concat(chunks);
      const frames = decodeGrpcFrames(body);
      const payload = frames.length ? frames[0].toString("utf8") : "";
      const status = trailers["grpc-status"] || trailers["Grpc-Status"] || "0";
      client.close();
      resolve({ status: Number(status), message: payload });
    });
    req.on("error", (err) => {
      client.close();
      reject(err);
    });

    const frame = encodeGrpcFrame(Buffer.from(message, "utf8"));
    req.end(frame);
  });
}

async function main() {
  const baseUrl =
    process.env.PHROND_BASE_URL ||
    (process.env.PHROND_HTTP_LISTEN
      ? `http://${process.env.PHROND_HTTP_LISTEN}`
      : "http://127.0.0.1:18000");
  const harness = createHarness({ baseUrl });

  await harness.startTestCase("grpc-basic", { suite: "examples" });

  const sutListen = process.env.SUT_LISTEN || "127.0.0.1:18095";

  const proxyInfo = await harness.startGrpcProxy({
    target: sutListen,
    insecure_skip_verify: true
  });
  const mockInfo = await harness.startGrpcMockServer({});

  await harness.registerGrpcStub({
    id: "alpha",
    path: "/example.Dependency/Alpha",
    response: {
      status: 0,
      body_base64: Buffer.from("alpha-ok", "utf8").toString("base64")
    }
  });

  await harness.registerGrpcStub({
    id: "beta",
    path: "/example.Dependency/Beta",
    response: {
      status: 0,
      body_base64: Buffer.from("beta-ok", "utf8").toString("base64")
    }
  });

  const sut = spawn("go", ["run", "./examples/grpc/sut"], {
    stdio: "inherit",
    env: {
      ...process.env,
      SUT_LISTEN: sutListen,
      DEPENDENCY_ADDR: mockInfo.addr
    }
  });

  const [sutHost, sutPort] = sutListen.split(":");
  const ready = await waitForPort(sutHost, Number(sutPort));
  if (!ready) {
    sut.kill();
    throw new Error("SUT failed to start");
  }

  const proxyListen = proxyInfo.listen_addr || proxyInfo.ListenAddr;
  if (!proxyListen) {
    throw new Error("gRPC proxy listen address not returned");
  }

  const result = await callGrpc(proxyListen, "/example.Service/Process", "ping");
  console.log("gRPC response:", result.status, result.message);

  const finish = await harness.finishTestCase("pass");
  console.log("artifact:", finish.path);

  sut.kill();
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
