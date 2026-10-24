const { captureJest } = require('@wiretap/sdk/jest');

captureJest({ app: require('../app') });
