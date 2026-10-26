# Check a local setup

Start with an existing HTTP application and its OpenAPI contract:

```sh
wiretap init --sut-url http://127.0.0.1:8080 \
  --schema schemas/orders.openapi.yaml --application orders
wiretap doctor
```

`init` creates `.wiretap.yaml` with application identity, a provided API named
`sut`, and automatic JSON/text/HTML coverage and Arazzo exports. The schema path is
relative to that configuration file. It creates no test runners, mocks, schemas
or application processes. Add dependencies and adjust API identity in the
ordinary [project configuration](configuration.md).

Existing files and symbolic links are never overwritten. Choose another new
path with `--output path`, or use `--output -` to print only the YAML for review
or use in a pipeline. Parent directories must already exist. `init` checks the
configuration's structure; `doctor` checks the supplied local contract contents.

## Inspect configuration without starting capture

```sh
wiretap doctor --config .wiretap.yaml
wiretap doctor --config .wiretap.yaml --format json
```

Doctor uses the runtime's project loader, environment overrides and configuration
validation. It reports missing environment substitutions, malformed local
OpenAPI contracts/references and unsupported boundary/output options. A missing
configuration produces an error with the initialization command to run. Projects
configured entirely through the runtime API can continue using that integration;
doctor inspects a project file.

Doctor reads local files and inspects the nearest existing output directory's
permission bits. It creates no directories, certificates, capture runs or probe
files, binds no listeners and sends no HTTP requests. Permission
bits are an indication: ACLs, filesystem mount restrictions and actual writes
are verified when capture starts. On Windows, only the parent directory's
existence/type is checked. Environment output-directory overrides are resolved
relative to the working directory, as they are at runtime.

Remote OpenAPI references appear as `deferred`. Missing optional
dependency specifications also appear as `deferred`, with their coverage limit.
The runtime resolves remote contracts when capture starts. Doctor does not
establish server availability, client CA trust, trace propagation, test ownership
or that requests actually cross Wiretap. Run the normal suite and inspect its
archive to verify those parts of the integration.

Exit `0` means the local checks passed, including any explicitly deferred checks.
Exit `1` means a reported check failed; the JSON report is still written to stdout.
Malformed command options return `2`. JSON output has `report_version: "1.0"`,
`ok`, `config_path` when resolved, and `checks` with `name`, `status` (`ok`,
`error` or `deferred`) and `message`.
