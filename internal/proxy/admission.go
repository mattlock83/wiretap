package proxy

import (
	"context"
	"net/http"
	"strings"

	"github.com/mattlock83/wiretap/internal/tracecontext"
	"go.opentelemetry.io/otel/trace"
)

// Admission binds an exchange to its owner before any upstream work begins.
// Release must be called even when routing or transport fails.
type Admission struct {
	SessionID       string
	CaseID          string
	TestCorrelation string
	Root            trace.SpanContext
	Record          func(HTTPExchange)
	RecordGRPC      func(GRPCExchange)
	GRPCFlowID      string
	TrackGRPC       func(context.CancelFunc, func()) func()
	Release         func()
}
type AdmissionResolver func(*http.Request, string) (*Admission, error)

func (s *Server) SetAdmissionResolver(resolver AdmissionResolver) {
	s.reverseMu.Lock()
	defer s.reverseMu.Unlock()
	s.admissionResolver = resolver
}

func (s *Server) admit(req *http.Request) (*Admission, error) {
	s.reverseMu.RLock()
	resolver := s.admissionResolver
	s.reverseMu.RUnlock()
	sessionID := strings.TrimSpace(req.Header.Get("X-Wiretap-Session-Id"))
	req.Header.Del("X-Wiretap-Session-Id")
	if resolver == nil {
		return &Admission{SessionID: sessionID}, nil
	}
	return resolver(req, sessionID)
}

func (s *Server) exchangeRecorder(req *http.Request, admission *Admission, dependency bool) func(HTTPExchange) {
	sc := tracecontext.Extract(req.Header)
	source := ""
	if sc.IsValid() {
		source = "received"
	} else if !dependency && admission.Root.IsValid() {
		sc = admission.Root
		tracecontext.Inject(req.Header, sc)
		source = "injected"
	}
	return func(exchange HTTPExchange) {
		exchange.SessionID = admission.SessionID
		exchange.CaseID = admission.CaseID
		exchange.TestCorrelation = admission.TestCorrelation
		exchange.TraceContextSource = source
		if sc.IsValid() {
			exchange.TraceID = sc.TraceID().String()
			exchange.RequestSpanID = sc.SpanID().String()
		}
		if admission.Record != nil {
			admission.Record(exchange)
		} else {
			s.recordHTTP(exchange)
		}
	}
}
