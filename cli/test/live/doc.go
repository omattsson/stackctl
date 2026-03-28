// Package live contains integration tests that run against a live backend.
// These tests are gated behind the "live" build tag and require environment
// variables STACKCTL_LIVE_URL, STACKCTL_LIVE_USER, and STACKCTL_LIVE_PASS.
//
// Run with: go test -tags live ./test/live/ -v
package live
