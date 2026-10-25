function printCompletionSummary(receipt) {
  if (receipt?.status !== 'complete' || !receipt.bundle_path || receipt.outputs?.errors?.length) return;
  const lines = [`Wiretap archive: ${receipt.bundle_path}`];
  for (const [format, filename] of Object.entries(receipt.outputs?.coverage || {}).sort()) {
    lines.push(`Wiretap coverage (${format}): ${filename}`);
  }
  for (const filename of receipt.outputs?.postman || []) lines.push(`Wiretap Postman: ${filename}`);
  // Runner reporters may emit JSON on stdout. Keep capture paths separate.
  process.stderr.write(`${lines.join('\n')}\n`);
}

module.exports = { printCompletionSummary };
