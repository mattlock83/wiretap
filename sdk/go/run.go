package wiretap

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// RunOptions configures the Wiretap process owned by a test package. Zero values
// discover .wiretap.yaml and use the wiretap executable on PATH (or WIRETAP_BINARY).
type RunOptions struct {
	Binary          string
	Config          string
	Dir             string
	OutputDir       string
	StartupTimeout  time.Duration
	FinishTimeout   time.Duration
	ShutdownTimeout time.Duration
	Outputs         *OutputOptions
	// Handler serves an application on an available local port until capture ends.
	Handler http.Handler
	// ExplicitSession requires tracing for concurrent attempts. Call StartTest
	// after t.Parallel and propagate context through application dependencies.
	ExplicitSession bool
}

type packageCapture struct {
	owner   *managedRuntime
	options RunOptions
	pkg     string
	sutURL  string
	mu      sync.Mutex
	attempt map[string]int
}

var activeCapture struct {
	sync.Mutex
	running bool
	current *packageCapture
}

// Run owns capture for a standard Go test package. Use os.Exit(wiretap.Run(m))
// in TestMain. It runs m.Run synchronously, finalizes exports after test cleanup,
// and returns the test exit code (or 1 if capture fails and tests passed).
// It never calls os.Exit and does not change global HTTP or tracing defaults.
func Run(m *testing.M, options ...RunOptions) int {
	if m == nil || len(options) > 1 {
		fmt.Fprintln(os.Stderr, "Wiretap Run needs a testing.M and at most one RunOptions value")
		return 1
	}
	var opts RunOptions
	if len(options) == 1 {
		opts = options[0]
	}
	return runPackage(m.Run, opts, testPackage(), startManaged)
}

func runPackage(run func() int, options RunOptions, pkg string, start func(context.Context, RunOptions) (*managedRuntime, error)) (code int) {
	activeCapture.Lock()
	if activeCapture.running {
		activeCapture.Unlock()
		fmt.Fprintln(os.Stderr, "Wiretap Run is already active in this test process")
		return 1
	}
	activeCapture.running = true
	activeCapture.Unlock()
	defer func() {
		activeCapture.Lock()
		activeCapture.current = nil
		activeCapture.running = false
		activeCapture.Unlock()
	}()
	state := &packageCapture{options: options, pkg: pkg, attempt: make(map[string]int)}
	if options.Handler != nil {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Wiretap application listener: %v\n", err)
			return 1
		}
		server := &http.Server{Handler: options.Handler, ReadHeaderTimeout: 10 * time.Second}
		state.sutURL = "http://" + listener.Addr().String()
		go func() { _ = server.Serve(listener) }()
		defer server.Close()
	}
	owner, err := start(context.Background(), options)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Wiretap startup failed: %v\n", err)
		return 1
	}
	state.owner = owner
	activeCapture.Lock()
	activeCapture.current = state
	activeCapture.Unlock()
	defer func() {
		receipt, err := owner.close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Wiretap finalization failed: %v\n", err)
			if code == 0 {
				code = 1
			}
		}
		fmt.Fprintf(os.Stderr, "Wiretap archive: %s\n", owner.info.BundlePath)
		if err == nil && receipt != nil && receipt.Outputs != nil {
			formats := make([]string, 0, len(receipt.Outputs.Coverage))
			for format := range receipt.Outputs.Coverage {
				formats = append(formats, format)
			}
			sort.Strings(formats)
			for _, format := range formats {
				fmt.Fprintf(os.Stderr, "Wiretap coverage (%s): %s\n", format, receipt.Outputs.Coverage[format])
			}
			for _, path := range receipt.Outputs.Postman {
				fmt.Fprintf(os.Stderr, "Wiretap Postman: %s\n", path)
			}
		}
	}()
	return run()
}

// StartTest captures one test and automatically records its final outcome during
// t.Cleanup. Call after t.Parallel and before registering application cleanup.
// Run must own the package; use Runtime.StartTest for an external runtime.
func StartTest(t testing.TB, options ...TestOptions) *Session {
	t.Helper()
	if len(options) > 1 {
		t.Fatal("Wiretap StartTest accepts at most one TestOptions value")
		return nil
	}
	activeCapture.Lock()
	state := activeCapture.current
	activeCapture.Unlock()
	if state == nil {
		t.Fatal("Wiretap StartTest requires wiretap.Run(m) in TestMain; use Runtime.StartTest for an externally managed runtime")
		return nil
	}
	var opts TestOptions
	if len(options) == 1 {
		opts = options[0]
	}
	return state.owner.runtime.StartTest(t, state.testOptions(t.Name(), opts))
}

func (state *packageCapture) testOptions(name string, opts TestOptions) TestOptions {
	if opts.SUTBaseURL == "" {
		opts.SUTBaseURL = state.sutURL
	}
	opts.ExplicitSession = opts.ExplicitSession || state.options.ExplicitSession
	identity := TestIdentity{}
	if opts.Identity == nil {
		identity.ID = "go:" + state.pkg + ":" + name
		if len(identity.ID) > 256 {
			identity.ID = fmt.Sprintf("go:sha256:%x", sha256.Sum256([]byte(identity.ID)))
		}
	} else {
		identity = *opts.Identity
	}
	state.mu.Lock()
	key := identity.ID + "\x00" + identity.Variant
	if identity.AttemptOrdinal == nil {
		state.attempt[key]++
		ordinal := state.attempt[key]
		identity.AttemptOrdinal = &ordinal
	} else if *identity.AttemptOrdinal > state.attempt[key] {
		state.attempt[key] = *identity.AttemptOrdinal
	}
	state.mu.Unlock()
	opts.Identity = &identity
	return opts
}

func testPackage() string {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, ".TestMain") {
			return strings.TrimSuffix(frame.Function, ".TestMain")
		}
		if !more {
			return "tests"
		}
	}
}
