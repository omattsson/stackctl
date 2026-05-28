// Package live contains integration tests that run against a live
// k8s-stack-manager backend. They are gated behind the "live" build tag
// because they make real HTTP calls and shape-check the response wire
// contract — the only way to catch field-name drift between stackctl and
// the backend that stub-based unit tests can't see.
//
// # Environment
//
//   - STACKCTL_LIVE_URL — backend base URL (default: http://localhost:8081).
//   - One of:
//       STACKCTL_LIVE_API_KEY                    — header-based, no session.
//       STACKCTL_LIVE_USER + STACKCTL_LIVE_PASS — login flow.
//   - STACKCTL_LIVE_HEAVY=1 — also run the workload tests in live_test.go
//     that actually deploy stack instances (~80 GiB of golden-db pull
//     each, takes ~10 min). Off by default — the per-endpoint *_live_test.go
//     files cover wire-shape contracts without real workloads.
//
// # Running locally
//
//	go test -tags live ./test/live/ -v
//
// # Reproducing the CI flow
//
// .github/workflows/live-tests.yml boots k8s-stack-manager (origin/main,
// api-only profile) in the same job and runs this suite. To reproduce
// locally, run the equivalent of those steps against your own checkout:
//
//	# 1. Boot the api-only stack (backend + mysql; no frontend).
//	cd /path/to/k8s-stack-manager
//	ADMIN_USERNAME=admin ADMIN_PASSWORD=ci-admin-password-do-not-reuse \
//	    docker compose up -d --wait backend
//
//	# 2. Apply the seed fixtures (one cluster, one definition, one
//	#    published template — so require* helpers don't skip).
//	docker exec -i app-mysql-dev mysql -u root -prootpassword app \
//	    < /path/to/stackctl/cli/test/live/testdata/ci-seed.sql
//
//	# 3. Mint an API key (login → mint), then run the suite.
//	jwt=$(curl -fsS -X POST http://localhost:8081/api/v1/auth/login \
//	    -H 'Content-Type: application/json' \
//	    -d '{"username":"admin","password":"ci-admin-password-do-not-reuse"}' \
//	    | jq -r '.token // .access_token')
//	admin_id=$(curl -fsS http://localhost:8081/api/v1/auth/me \
//	    -H "Authorization: Bearer $jwt" | jq -r '.id')
//	key=$(curl -fsS -X POST "http://localhost:8081/api/v1/users/$admin_id/api-keys" \
//	    -H "Authorization: Bearer $jwt" -H 'Content-Type: application/json' \
//	    -d '{"name":"local-live-tests","expires_in_days":1}' | jq -r '.raw_key')
//
//	STACKCTL_LIVE_URL=http://localhost:8081 \
//	STACKCTL_LIVE_API_KEY="$key" \
//	    go test -tags live -count=1 ./test/live/...
package live
