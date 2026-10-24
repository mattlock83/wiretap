const tracing = require("./tracing");
const DEFAULT_BASE_URL = "http://127.0.0.1:18000";

class CoreClient {
  constructor(config = {}) {
    this.baseUrl = config.baseUrl || process.env.WIRETAP_BASE_URL || DEFAULT_BASE_URL;
    this.timeoutMs = config.timeoutMs ?? 30000;
    if (!Number.isSafeInteger(this.timeoutMs) || this.timeoutMs < 0 || this.timeoutMs > 2147483647) {
      throw new Error("timeoutMs must be an integer between 0 and 2147483647 (0 disables the control request deadline)");
    }
    this.explicitTimeout = config.timeoutMs !== undefined;
    this.requireJSON = config.requireJSON === true;
  }

  startRun(payload, options) { return this._request("POST", "/v1/startRun", payload || {}, options); }
  setupTest(payload, options) { return this._request("POST", "/v1/setupTest", payload || {}, options); }
  teardownTest(payload, options) { return this._request("POST", "/v1/teardownTest", payload || {}, options); }
  report(payload, options) { return this._request("POST", "/v1/report", payload || {}, options); }
  finishRun(payload = {}, options) { return this._request("POST", "/v1/finishRun", payload, options); }

  async _request(method, path, payload, { signal } = {}) {
    if (typeof fetch !== "function") throw new Error("global fetch is not available; require Node.js >= 18");
    const options = { method, headers: { "content-type": "application/json" } };
    if (payload !== undefined) options.body = JSON.stringify(payload);
    // A default cleanup deadline must allow the requested runtime drain. An
    // explicitly configured deadline (including zero) always belongs to the caller.
    const timeoutMs = !this.explicitTimeout && path === "/v1/teardownTest"
      ? Math.max(this.timeoutMs, (payload?.drain_timeout_ms ?? 5000) + 10000)
      : this.timeoutMs;
    let timeout, controller, deadlineError;
    if (signal?.aborted) throw signal.reason || new Error("Wiretap control request was cancelled");
    if (timeoutMs > 0 || signal) {
      controller = new AbortController();
      options.signal = controller.signal;
    }
    const abort = () => controller.abort(signal.reason);
    signal?.addEventListener("abort", abort, { once: true });
    if (timeoutMs > 0) {
      timeout = setTimeout(() => {
        deadlineError = new Error(`Wiretap control request ${method} ${path} timed out after ${timeoutMs}ms`);
        deadlineError.name = "TimeoutError";
        deadlineError.code = "WIRETAP_CONTROL_TIMEOUT";
        controller.abort(deadlineError);
      }, timeoutMs);
    }
    try {
      const res = await tracing.withoutTracing(() => fetch(new URL(path, this.baseUrl), options));
      const text = await res.text();
      let body = text || null;
      if (this.requireJSON || (text && (res.headers.get("content-type") || "").includes("application/json"))) {
        try { body = JSON.parse(text); }
        catch (cause) {
          const error = new Error(`Wiretap control request ${method} ${path} returned an invalid JSON response (${res.status})`, { cause });
          error.status = res.status;
          throw error;
        }
      }
      if (!res.ok) {
        const err = new Error(`request failed: ${res.status}${body?.error ? `: ${body.error}` : ""}`);
        err.status = res.status;
        err.body = body;
        throw err;
      }
      return body;
    } catch (error) {
      throw deadlineError || error;
    } finally {
      if (timeout) clearTimeout(timeout);
      signal?.removeEventListener("abort", abort);
    }
  }
}


module.exports = { CoreClient };
