#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"

run_package_cases() {
	local package="$1"
	local test_pattern="$2"
	printf 'LAB-002 regression cases: %s\n' "$package"
	go test -race "$package" -run "$test_pattern" -count=1
}

# These tests use httptest, test-owned loopback sockets, injected clocks and
# temporary state. The audit crash test kills only its own helper process.
# This script does not alter host time, fill a filesystem, contact providers,
# or touch a running deployment.
run_package_cases ./internal/proxy '^(TestSilentUpstreamTimesOutWith504|TestUnhealthyUpstreamReturns503|TestUpstream5xxDoesNotPreventRecovery|TestRequiredAccessAuditFailureStopsBeforeUpstream)$'
run_package_cases ./internal/tunnel '^(TestTunnelHTTPConcurrencyTrustAndReconnect|TestTunnelRejectsUnauthorizedTLS|TestRequestCancellationClosesOnlyItsStream)$'
run_package_cases ./internal/tunnel '^(TestConnectorReattachesAfterEdgeRestart|TestRevocationClosesLiveSessionAndBlocksReconnect)$'
run_package_cases ./internal/certs '^(TestRenewalDue|TestManagerFailsClosedWithoutCertificate|TestManagerKeepsServingAfterFailedRenewal|TestManagerServesPersistedCertificateAfterRestart)$'
run_package_cases ./cmd/switchboard '^(TestAuditFailureReporterReturnsFailureAndLogsOnce|TestAuditFailureReporterStatusTracksStorageFailuresOnly)$'
run_package_cases ./internal/store/sqlite '^(TestReadOnlyStatusDoesNotCreateMigrationTable|TestOpenReadOnlyAndIntegrityCheck|TestOpenReadOnlyDoesNotCreateMissingFile)$'
run_package_cases ./internal/audit '^TestProcessKillAfterAppendCanRestartAndVerify$'
