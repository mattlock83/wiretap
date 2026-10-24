const { withWiretap } = require('../../sdk/js/mocha');

module.exports = withWiretap({
  spec: [`${__dirname}/orders.test.cjs`],
}, {
  cwd: __dirname,
  app: './app.cjs',
});
