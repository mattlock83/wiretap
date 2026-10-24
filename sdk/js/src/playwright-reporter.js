const { getRun, attemptKey } = require('./playwright-state');

module.exports = class WiretapReporter {
  printsToStdio() { return false; }
  onBegin() { const run = getRun(); if (run) run.reporterConnected = true; }
  onTestEnd(test, result) {
    const run = getRun();
    // A reporter can also be loaded for --list or merge-reports, which have no
    // managed runtime and must not start one.
    if (!run) return;
    const captured = run.finish(attemptKey(test.id, result.retry), {
      status: result.status === 'passed' ? 'pass' : result.status === 'skipped' ? 'skip' : 'fail',
      error: result.error,
    });
    if (!captured && result.status !== 'skipped') {
      run.failures.push(new Error(`Test "${test.title}" did not use Wiretap fixtures; import test from @wiretap/sdk/playwright or use capturePlaywright(existingTest)`));
    }
  }
};
