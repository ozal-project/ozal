package admin

import (
	"net/http"
	"strings"
	"testing"

	"github.com/switchboard-project/switchboard/internal/config"
)

func TestStatusShowsCertificateLifecycle(t *testing.T) {
	manager := &fakeManager{cfg: config.Config{Admin: config.Admin{Token: readToken}}}
	status := CertificateStatus{
		Host:        "preview.example.com",
		Mode:        "ACME DNS-01",
		Valid:       false,
		LastOutcome: "failed",
		LastError:   "acme: simulated outage",
		LastAttempt: "2026-10-18T10:00:00Z",
		NextAttempt: "2026-10-18T10:05:00Z",
		Pending:     2,
	}
	handler := New(manager, nil, WithCertificateStatus(func() CertificateStatus { return status }))

	rec := request(handler, http.MethodGet, "/_/status", readToken, "")
	body := rec.Body.String()
	for _, want := range []string{
		"Origin certificate",
		"preview.example.com",
		"ACME DNS-01",
		"Fail closed",
		"failed",
		"acme: simulated outage",
		"2026-10-18T10:05:00Z",
		"DNS records pending cleanup",
		"readiness must not be reported as healthy",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
}

func TestStatusOmitsCertificatePanelWithoutProvider(t *testing.T) {
	handler := New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: readToken}}}, nil)
	rec := request(handler, http.MethodGet, "/_/status", readToken, "")
	if strings.Contains(rec.Body.String(), "Origin certificate") {
		t.Fatal("autocert mode must not render a DNS-01 certificate panel")
	}
}
