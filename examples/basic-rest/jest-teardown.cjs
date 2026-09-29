// Jest invokes global teardown after every worker has finished. This uses the
// same HTTP lifecycle available to any language or test framework.
module.exports = async () => {
  const response = await fetch(`${process.env.WIRETAP_BASE_URL}/v1/finishRun`, {
    method: 'POST', headers: { 'content-type': 'application/json' }, body: '{}'
  });
  if (!response.ok) throw new Error(`Wiretap finalization failed: ${await response.text()}`);
};
