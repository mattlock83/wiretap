const { withWiretap } = require('@wiretap/sdk/jest');

module.exports = withWiretap({
  ...require('./package.json').jest,
  setupFilesAfterEnv: ['<rootDir>/wiretap/setup.cjs'],
});
