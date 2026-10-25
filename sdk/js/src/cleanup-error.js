// Keep the runner's original thrown value and expose every later cleanup failure.
// Frozen errors and primitive throws still need a diagnostic the runner can show.
function retainCleanupError(primary, secondary) {
  if (primary === secondary) return;
  try {
    if (primary && (typeof primary === 'object' || typeof primary === 'function')) {
      const errors = primary.cleanupErrors || (primary.cleanupError ? [primary.cleanupError] : []);
      primary.cleanupErrors = [...errors, secondary];
      primary.cleanupError ??= secondary;
      if (primary.cleanupErrors.includes(secondary)) return;
    }
  } catch { /* Preserve a frozen or otherwise caller-owned thrown value. */ }
  console.error('Wiretap cleanup also failed:', secondary);
}

module.exports = { retainCleanupError };
