const key = Symbol.for('@wiretap/sdk/playwright/run');
module.exports = {
  getRun: () => globalThis[key],
  setRun: run => { globalThis[key] = run; },
  attemptKey: (id, retry) => `${id}:${retry}`,
};
