# Examples

Run these commands from the repository root. Each example writes a capture
bundle and a separate exploration bundle, and prints their paths. Open
`viewer/index.html` for the dashboard or `index.md` for the OKF pages.

| Example | Demonstrates | Requirements |
| --- | --- | --- |
| [Basic REST](basic-rest/README.md) | Existing Node tests and an HTTP dependency mock; optional parallel Jest capture; generated invalid-input checks. | Go 1.24+, Node.js 20.12+, Bash and curl. |
| [Provider exploration](../docs/exploration.md#try-the-complete-example) | SQLite fixtures, fresh authentication, cleanup and side-effect assertions through Python hooks. | Go 1.24+ and Python 3.9+. |
| [Microsvc Transactions](microsvc-transactions/README.md) | A real service image, an Accounts mock, disposable database state and authenticated exploration. | Go 1.24+, Python 3.9+, Docker and the documented local images. |

## Basic REST

```bash
npm ci --prefix sdk/js
./examples/run-basic-rest-e2e.sh
```

For parallel Jest capture against the instrumented demo application:

```bash
WIRETAP_TEST_RUNNER=jest ./examples/run-basic-rest-e2e.sh
```

## Provider exploration with database hooks

```bash
python3 examples/provider-exploration/demo.py
```

## Microsvc Transactions

```bash
./examples/run-microsvc-transactions.sh
```

Read the [Microsvc guide](microsvc-transactions/README.md) for image requirements,
contract scope and replaying an existing capture.
