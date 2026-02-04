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

  async startTestCase(name, meta) {
    return this._post("/v1/testcases/start", { name, meta });
  }

  async finishTestCase(status) {
    return this._post("/v1/testcases/finish", { status });
  }

  async getArtifact() {
    return this._get("/v1/artifact");
  }

  async startProxy(config) {
    return this._post("/v1/proxy/start", config || {});
  }

  async startMockServer(config) {
    return this._post("/v1/mock/start", config || {});
  }

  async registerStub(stub) {
    return this._post("/v1/stubs", stub);
  }

  async clearStubs() {
    return this._post("/v1/stubs/clear", {});
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
