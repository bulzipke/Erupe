package api

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestAPIServerUsesConfiguredAccessLogWriter(t *testing.T) {
	var access bytes.Buffer
	c := NewTestConfig()
	c.API.Port = 0
	s := NewAPIServer(&Config{Logger: zap.NewNop(), ErupeConfig: c, AccessLogWriter: &access})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown()
	s.httpServer.Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/version", nil))
	if !strings.Contains(access.String(), "GET /version") {
		t.Fatal("HTTP access log bypassed the configured writer")
	}
	before := access.Len()
	s.httpServer.Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/health", nil))
	if access.Len() != before {
		t.Fatal("health probes should remain excluded from access logs")
	}
}
