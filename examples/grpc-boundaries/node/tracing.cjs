// Application-owned standard OTel setup. Load before any gRPC or HTTP modules.
const api = require('@opentelemetry/api');
const { BasicTracerProvider } = require('@opentelemetry/sdk-trace-base');
const { AsyncLocalStorageContextManager } = require('@opentelemetry/context-async-hooks');
const { W3CTraceContextPropagator } = require('@opentelemetry/core');
const { registerInstrumentations } = require('@opentelemetry/instrumentation');
const { GrpcInstrumentation } = require('@opentelemetry/instrumentation-grpc');
const { HttpInstrumentation } = require('@opentelemetry/instrumentation-http');
const provider = new BasicTracerProvider();
const contextManager = new AsyncLocalStorageContextManager().enable();
api.trace.setGlobalTracerProvider(provider);
api.context.setGlobalContextManager(contextManager);
api.propagation.setGlobalPropagator(new W3CTraceContextPropagator());
registerInstrumentations({ instrumentations: [new GrpcInstrumentation(), new HttpInstrumentation()] });
module.exports = { api, provider, contextManager };
