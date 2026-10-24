"""Run the real Microsvc Transactions image with isolated example fixtures."""

import argparse
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import tempfile
import time

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent.parent


def command(args, **kwargs):
    return subprocess.run(args, check=True, text=True, **kwargs)


def docker(*args):
    return command(["docker", *args], stdout=subprocess.PIPE).stdout.strip()


def wait_for(container, url):
    # All test traffic stays on Docker's internal network, including readiness.
    docker("exec", container, "python3", "-c", """
import sys, time
from urllib.request import urlopen
from urllib.error import URLError
deadline = time.monotonic() + 120
while time.monotonic() < deadline:
    try:
        with urlopen(sys.argv[1], timeout=2) as response:
            if response.status == 200:
                sys.exit(0)
    except (URLError, TimeoutError, ConnectionError):
        pass
    time.sleep(.5)
sys.exit('Service did not become ready: ' + sys.argv[1])
""", url)


def inspect_bundle(path):
    run = json.loads((path / "bundle.json").read_text())
    if run["run_state"] != "complete":
        raise RuntimeError(f"Incomplete bundle: {path}")
    coverage = json.loads((path / "exports" / "coverage.json").read_text())["contract_coverage"]
    if not any(s["api_id"] == "accounts" and s["target_kind"] == "mock" for s in coverage["specs"]):
        raise RuntimeError("Bundle is missing the Accounts mock contract")
    downstream = 0
    for attempt in run["tests"]:
        capture = json.loads((path / attempt["path"]).read_text())["capture"]
        if capture.get("capture_incomplete"):
            raise RuntimeError("Incomplete capture; inspect the bundle")
        if capture.get("schema_validation", {}).get("status") == "fail":
            print("Contract mismatch recorded; inspect validation evidence alongside the framework outcome.")
        downstream += sum(f["channel"] == "sut_to_dependency" for f in capture["traffic"])
    if downstream < 5:
        raise RuntimeError(f"Expected at least five captured Accounts calls, found {downstream}")
    print(f"Verified {downstream} captured Accounts exchanges.")


def run(args):
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    transactions_image = os.environ.get("TRANSACTIONS_IMAGE", "microsvc_transactions-service:latest")
    fraud_image = os.environ.get("FRAUD_IMAGE", "microsvc_fraud-service:latest")
    postgres_image = os.environ.get("POSTGRES_IMAGE", "postgres:15-alpine")
    # Reuse images only; never restart or reconfigure the user's existing stack.
    image_ids = {}
    for image in (transactions_image, fraud_image, postgres_image):
        try:
            image_ids[image] = docker("image", "inspect", "--format", "{{.Id}}", image)
        except subprocess.CalledProcessError:
            raise RuntimeError(f"Required local image {image!r} is missing; see {HERE / 'README.md'}") from None

    suffix = secrets.token_hex(4)
    project = "wiretap-transactions-" + suffix
    utility_image = project + ":example"
    containers = []
    bundles = []
    network_created = False
    image_created = False
    jwt_secret = secrets.token_hex(32)
    log_dir = output / ("logs-" + suffix)
    log_dir.mkdir()
    (log_dir / "images.json").write_text(json.dumps(image_ids, indent=2) + "\n")
    failure = None
    copied = False

    def start(role, image, env=None, extra=(), cmd=()):
        name = project + "-" + role
        argv = ["run", "-d", "--name", name, "--label", "wiretap.example=" + project,
                "--network", project, "--network-alias", role]
        for key, value in (env or {}).items():
            argv.extend(["-e", f"{key}={value}"])
        containers.append(name)  # Also clean up a created-but-failed container.
        docker(*argv, *extra, image, *cmd)
        return name

    def logs(name, filename=None):
        result = subprocess.run(["docker", "logs", name], text=True, capture_output=True)
        # This isolated example uses disposable keys and synthetic test credentials.
        (log_dir / (filename or name + ".log")).write_text(result.stdout + result.stderr)

    def start_transactions(accounts_url):
        return start("transactions", transactions_image, {
            "DB_HOST": "postgres", "DB_NAME": "banking_app", "JWT_SECRET": jwt_secret,
            "ACCOUNTS_SERVICE_URL": accounts_url, "FRAUD_SERVICE_HOST": "fraud",
            "FRAUD_SERVICE_PORT": "8080", "KAFKA_BROKERS": "127.0.0.1:9092",
            "SPRING_KAFKA_PRODUCER_PROPERTIES_MAX_BLOCK_MS": "500",
            "SPRING_KAFKA_PRODUCER_PROPERTIES_REQUEST_TIMEOUT_MS": "1000",
            "SPRING_KAFKA_PRODUCER_PROPERTIES_DELIVERY_TIMEOUT_MS": "1000",
            # Microsvc's JWT filter skips ERROR dispatches. Keep normal requests
            # authenticated without replacing Spring's input errors with 401.
            "SPRING_SECURITY_FILTER_DISPATCHER_TYPES": "request,async,forward,include",
            "ERROR_SPIKE_ENABLED": "false", "DEMO_MEMO_BUG_ENABLED": "false",
            "DEMO_CONTRACT_REFACTOR_ENABLED": "false",
            "OTEL_TRACES_EXPORTER": "none", "OTEL_METRICS_EXPORTER": "none", "OTEL_LOGS_EXPORTER": "none",
        }, cmd=("--logging.level.com.banking.transactionsservice=WARN",
                               "--logging.level.org.springframework.web=WARN",
                               "--logging.level.org.springframework.security=WARN"))

    with tempfile.TemporaryDirectory(prefix="wiretap-transactions-") as temporary:
        work = Path(temporary)
        try:
            print("Building Wiretap and the example fixture image...", flush=True)
            arch = docker("info", "--format", "{{.Architecture}}")
            arch = {"x86_64": "amd64", "aarch64": "arm64"}.get(arch, arch)
            environment = {**os.environ, "GOOS": "linux", "GOARCH": arch, "CGO_ENABLED": "0"}
            command(["go", "build", "-o", str(work / "wiretap"), "./cmd/wiretap"], cwd=ROOT, env=environment)
            shutil.copytree(HERE, work / "example", ignore=shutil.ignore_patterns("__pycache__", "*.pyc"))
            shutil.copyfile(HERE / "Dockerfile", work / "Dockerfile")
            docker("build", "-q", "-t", utility_image, str(work))
            image_created = True
            # No Internet egress: background payment calls cannot contact real providers.
            docker("network", "create", "--internal", "--label", "wiretap.example=" + project, project)
            network_created = True
            postgres = start("postgres", postgres_image,
                             {"POSTGRES_DB": "banking_app", "POSTGRES_PASSWORD": "wiretap-example"},
                             extra=("--tmpfs", "/var/lib/postgresql/data"))
            for _ in range(120):
                ready = subprocess.run(["docker", "exec", postgres, "pg_isready", "-U", "postgres"],
                                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                if ready.returncode == 0:
                    break
                time.sleep(.5)
            else:
                raise RuntimeError("Example PostgreSQL did not start")
            command(["docker", "exec", "-i", postgres, "psql", "-U", "postgres", "-d", "banking_app", "-v", "ON_ERROR_STOP=1"],
                    input="CREATE USER transactions_service_user WITH PASSWORD 'transactions_service_pass';\n"
                          "CREATE SCHEMA transactions_service AUTHORIZATION transactions_service_user;\n",
                    stdout=subprocess.DEVNULL)
            accounts = start("accounts", utility_image)
            wait_for(accounts, "http://accounts:8080/healthz")
            start("fraud", fraud_image)
            runtime = start("wiretap", utility_image, {"DEMO_JWT_SECRET": jwt_secret},
                            cmd=("wiretap", "--config", "/example/.wiretap.yaml"))
            wait_for(accounts, "http://wiretap:18000/healthz")
            sut = start_transactions("http://wiretap:18080/__wiretap/dep/accounts")
            wait_for(accounts, "http://transactions:8080/actuator/health")
            print("Running the existing unittest suite...", flush=True)
            tests = subprocess.run(["docker", "exec", "-e", "WIRETAP_BASE_URL=http://wiretap:18000",
                                    "-e", "WIRETAP_SUT_URL=http://wiretap:18080", "-e", "TRANSACTIONS_IMAGE=" + transactions_image,
                                    "-e", "TRANSACTIONS_IMAGE_ID=" + image_ids[transactions_image],
                                    "-e", "CAPTURE_PATH_FILE=/capture.txt", runtime, "python3", "test_transactions.py"])
            source_result = subprocess.run(["docker", "exec", runtime, "cat", "/capture.txt"], text=True, capture_output=True)
            if source_result.returncode == 0:
                capture_path = source_result.stdout.strip()
                bundles.append(("Combined", capture_path))
                docker("exec", runtime, "mkdir", "-p", capture_path + "/exports")
                docker("exec", runtime, "wiretap", "report", "--bundle", capture_path,
                       "--format", "json", "--output", capture_path + "/exports/coverage.json")
            if tests.returncode:
                raise RuntimeError("Tests failed; inspect the bundle and SUT logs")

        except (RuntimeError, subprocess.CalledProcessError, KeyboardInterrupt) as error:
            failure = error
        finally:
            # Preserve completed or failed evidence before disposing of the harness.
            if "runtime" in locals():
                copied = subprocess.run(["docker", "cp", runtime + ":/artifacts/.", str(output)],
                                        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0
            if copied:
                for label, path in bundles:
                    print(f"{label} raw capture: {output / Path(path).name}")
                    print(f"{label} JSON coverage: {output / Path(path).name / 'exports' / 'coverage.json'}")
            for name in reversed(containers):
                logs(name)
                subprocess.run(["docker", "rm", "-f", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            if network_created:
                subprocess.run(["docker", "network", "rm", project], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            if image_created:
                subprocess.run(["docker", "image", "rm", utility_image], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            print("Example logs:", log_dir)
    if bundles and not copied:
        raise RuntimeError("Could not copy Docker bundle output; see the example logs")
    for label, path in bundles:
        inspect_bundle(output / Path(path).name)
    if failure is not None:
        raise failure


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=ROOT / "artifacts/microsvc-transactions")
    try:
        run(parser.parse_args())
    except (RuntimeError, subprocess.CalledProcessError, KeyboardInterrupt) as error:
        print(f"Example failed: {error}", file=sys.stderr)
        sys.exit(1)
