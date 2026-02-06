const DEFAULT_BASE_URL = "http://127.0.0.1:18000";

class Harness {
  constructor(config = {}) {
    this.baseUrl = config.baseUrl || DEFAULT_BASE_URL;
    this.timeoutMs = config.timeoutMs || 0;
  }

  async healthz() {
    return this._get("/healthz");
  }

  async startLifecycle() {
    return this._post("/v1/lifecycle/start", {});
  }

  async stopLifecycle() {
    return this._post("/v1/lifecycle/stop", {});
  }

  async startTestCase(name, meta, options = {}) {
    if (!options.schemas || options.schemas.length === 0) {
      throw new Error("schemas are required; pass options.schemas");
    }
    const payload = { name, meta };
    if (options.tags) {
      payload.tags = options.tags;
    }
    if (options.sutBaseUrl) {
      payload.sut_base_url = options.sutBaseUrl;
    }
    if (options.schemas) {
      payload.schemas = options.schemas;
    }
    return this._post("/v1/testcases/start", payload);
  }

  async finishTestCase(status) {
    const resp = await this._post("/v1/testcases/finish", { status });
    if (resp && resp.schema_validation && !resp.schemaValidation) {
      resp.schemaValidation = resp.schema_validation;
    }
    if (resp && resp.artifact && resp.artifact.meta && resp.artifact.meta.schema_validation && !resp.schemaValidation) {
      resp.schemaValidation = resp.artifact.meta.schema_validation;
    }
    return resp;
  }

  async getArtifact() {
    return this._get("/v1/artifact");
  }

  async downloadArtifact(filePath) {
    if (!filePath) {
      throw new Error("filePath is required");
    }
    if (typeof fetch !== "function") {
      throw new Error("global fetch is not available; require Node.js >= 18");
    }

    const url = new URL("/v1/artifact", this.baseUrl);
    const res = await fetch(url);
    if (!res.ok) {
      const text = await res.text();
      const err = new Error(`artifact download failed: ${res.status}`);
      err.status = res.status;
      err.body = text;
      throw err;
    }

    const { writeFile } = require("node:fs/promises");
    const buffer = Buffer.from(await res.arrayBuffer());
    await writeFile(filePath, buffer);
    return { path: filePath };
  }

  async waitForStatus(options = {}) {
    const intervalMs = options.intervalMs || 500;
    const timeoutMs = options.timeoutMs || 30_000;
    const start = Date.now();

    while (true) {
      const status = await this._get("/v1/testcases/status");
      if (!status.active) {
        return status;
      }
      if (Date.now() - start > timeoutMs) {
        const err = new Error("timed out waiting for test case to finish");
        err.status = status;
        throw err;
      }
      await new Promise((resolve) => setTimeout(resolve, intervalMs));
    }
  }

  async startProxy(config) {
    return this._post("/v1/proxy/start", config || {});
  }

  async startMockServer(config) {
    return this._post("/v1/mock/start", config || {});
  }

  async startGrpcProxy(config) {
    return this._post("/v1/grpc/proxy/start", config || {});
  }

  async startGrpcMockServer(config) {
    return this._post("/v1/grpc/mock/start", config || {});
  }

  async registerStub(stub) {
    return this._post("/v1/stubs", stub);
  }

  async clearStubs() {
    return this._post("/v1/stubs/clear", {});
  }

  async registerGrpcStub(stub) {
    return this._post("/v1/grpc/stubs", stub);
  }

  async clearGrpcStubs() {
    return this._post("/v1/grpc/stubs/clear", {});
  }

  async assertField(assertion) {
    return this._post("/v1/assertions", assertion);
  }

  async expectCall(expectation) {
    return this._post("/v1/expectations", expectation);
  }

  async setSchema(schema) {
    return this._post("/v1/schemas", schema);
  }

  async addRedaction(rule) {
    return this._post("/v1/redactions", rule);
  }

  async setProxyMode(mode) {
    return this._post("/v1/proxy/mode", { mode });
  }

  async getCaCertificate() {
    return this._get("/v1/proxy/ca");
  }

  async _get(path) {
    return this._request("GET", path);
  }

  async _post(path, payload) {
    return this._request("POST", path, payload);
  }

  async _request(method, path, payload) {
    if (typeof fetch !== "function") {
      throw new Error("global fetch is not available; require Node.js >= 18");
    }

    const url = new URL(path, this.baseUrl);
    const options = {
      method,
      headers: {
        "content-type": "application/json"
      }
    };

    if (payload && method !== "GET") {
      options.body = JSON.stringify(payload);
    }

    let controller;
    if (this.timeoutMs > 0) {
      controller = new AbortController();
      options.signal = controller.signal;
      setTimeout(() => controller.abort(), this.timeoutMs).unref?.();
    }

    const res = await fetch(url, options);
    const text = await res.text();
    const contentType = res.headers.get("content-type") || "";
    const isJSON = contentType.includes("application/json");
    const body = text ? (isJSON ? JSON.parse(text) : text) : null;

    if (!res.ok) {
      const err = new Error(`request failed: ${res.status}`);
      err.status = res.status;
      err.body = body;
      throw err;
    }

    return body;
  }
}

function createHarness(config) {
  return new Harness(config);
}

module.exports = {
  Harness,
  createHarness
};
