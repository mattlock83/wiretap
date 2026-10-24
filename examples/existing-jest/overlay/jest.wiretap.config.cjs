const { withWiretap } = require('@wiretap/sdk/jest');

module.exports = withWiretap(require('./package.json').jest, {
  app: '<rootDir>/app.js',
});
