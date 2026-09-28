// node:test exposes final hook/subtest errors through TestContext. Keep its
// actual test tree intact: an extra wrapper subtest would run inherited hooks
// twice and change the user's test names.
async function runNodeTest(runtime, t, name, fn, defaults) {
  if (typeof t.passed !== 'boolean') throw new Error('The Phrond node:test adapter requires Node 20.12 or newer');
  await Promise.all(runtime.pendingCleanup);
  if (t.signal.aborted) throw t.signal.reason || new Error('test already completed');
  const attempt = runtime.createTestAttempt(t.fullName || name, defaults);
  let skipped = false, childFailure, generation = 0;
  const finish = () => attempt.finish({
    status: t.error || childFailure ? 'fail' : skipped ? 'skip' : 'pass',
    error: t.error || childFailure
  });
  const after = t.after.bind(t);
  const registerFinish = () => {
    const current = generation;
    after(() => current === generation ? finish() : undefined,
      { timeout: (defaults.drainTimeoutMs ?? 5000) + 30000 });
  };
  t.after = (callback, options) => {
    generation++;
    after(callback, options);
    registerFinish();
  };
  registerFinish();
  const skip = t.skip.bind(t);
  t.skip = (...args) => { skipped = true; return skip(...args); };
  const subtest = t.test.bind(t);
  t.test = (...args) => {
    let child, todo = args.some(arg => arg && typeof arg === 'object' && arg.todo);
    const index = args.findLastIndex(arg => typeof arg === 'function');
    if (index >= 0) {
      const callback = args[index];
      args[index] = function (...values) {
        child = values[0];
        const markTodo = child.todo.bind(child);
        child.todo = (...values) => { todo = true; return markTodo(...values); };
        return callback.apply(this, values);
      };
      Object.defineProperty(args[index], 'length', { value: callback.length });
    }
    return subtest(...args).then(value => {
      if (!todo && child?.error) childFailure ||= child.error;
      return value;
    });
  };
  // A failed user after-hook can prevent later hooks from running. The signal
  // also fires for timeouts whose callback never settles. In these cases the
  // runner already owns the failure, but capture must still be finalized.
  t.signal.addEventListener('abort', () => {
    const pending = finish().catch(error => t.diagnostic(`Phrond cleanup: ${error.message}`));
    runtime.pendingCleanup.add(pending);
    pending.then(() => runtime.pendingCleanup.delete(pending));
  }, { once: true });
  return attempt.run(session => fn(t, session));
}

module.exports = { runNodeTest };
