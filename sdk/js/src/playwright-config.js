const metadataKey = '__wiretapPlaywright';

function withWiretap(config = {}, options = {}) {
  if (process.argv.slice(2).some(argument => /^--ui(?:=|$)|^--ui-(?:host|port)(?:=|$)/.test(argument))) {
    throw new Error('Wiretap capture requires a single Playwright run; remove --ui, --ui-host and --ui-port and use playwright test');
  }
  if (config.metadata?.[metadataKey]) throw new Error('withWiretap was applied to this Playwright configuration twice');
  if (options.signal) throw new Error('Playwright owns cancellation; signal is only supported by startManagedRuntime');
  if (options.capture?.instrumentation !== undefined) throw new Error('Playwright carries trace context through its request and browser fixtures; configure custom transport instrumentation in your fixtures instead of capture.instrumentation');
  if (options.capture?.tracerProvider !== undefined || options.capture?.onSession !== undefined || options.capture?.testDefaults?.onSession !== undefined) {
    throw new Error('Playwright capture options must be serializable; use Playwright fixtures for session hooks or the plain JavaScript SDK for custom tracer providers');
  }
  if ((config.fullyParallel || (config.workers !== undefined && config.workers !== 1)) && !options.explicitSession) {
    throw new Error('Parallel Playwright capture requires explicitSession: true and W3C trace propagation in the application; otherwise use workers: 1');
  }
  const wrapUse = (use = {}, inherited = {}) => {
    const { baseURL, extraHTTPHeaders, wiretapOptions = {}, ...rest } = use;
    return { ...rest, wiretapOptions: {
      ...inherited,
      ...(baseURL !== undefined ? { sutBaseUrl: baseURL } : {}),
      ...(extraHTTPHeaders !== undefined ? { headers: extraHTTPHeaders } : {}),
      ...wiretapOptions,
    } };
  };
  const use = wrapUse(config.use);
  const reporter = config.reporter === undefined ? [[process.env.CI ? 'dot' : 'list']]
    : typeof config.reporter === 'string' ? [[config.reporter]] : config.reporter;
  const setup = config.globalSetup ? (Array.isArray(config.globalSetup) ? config.globalSetup : [config.globalSetup]) : [];
  return {
    ...config,
    workers: config.workers ?? 1,
    use,
    ...(config.projects ? { projects: config.projects.map(project => ({ ...project, use: wrapUse(project.use, use.wiretapOptions) })) } : {}),
    metadata: { ...config.metadata, [metadataKey]: options },
    globalSetup: [...setup, require.resolve('./playwright-global-setup')],
    reporter: [...reporter, [require.resolve('./playwright-reporter')]],
  };
}
module.exports = { withWiretap, metadataKey };
