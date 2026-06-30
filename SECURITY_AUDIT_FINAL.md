# Vector v6.0.0-rc5 — Deep Source, Security, Reliability, and Release Audit

Audit date: 2026-06-29

## Executive conclusion

The original uploaded archive was not safe to publish. Multiple independent passes found authentication, setup, routing, data-consistency, dependency, installer, and release-integrity defects. Every reproducible defect listed as **Fixed** in this report has been corrected in this source tree and covered by automated regression tests where practical.

No source review can honestly guarantee that an application has zero undiscovered defects. This tree has passed the full local validation described below, but it is still source code—not an official signed release. The official Linux AMD64 artifact must be produced by the included clean GitHub Actions workflow with Go 1.26.4 and then verified against its checksum and provenance attestation.

## Fixed findings

### Critical

1. **Generated Argon2id password hashes could never verify.** The generator emitted the standard five-field encoded form, while the verifier required six fields. Newly created administrator and protected-link passwords were unusable. The parser and regression tests are corrected.
2. **Security-critical routing could fail open during a database outage.** Failed reads of `setup_complete` or the primary domain could become empty strings, temporarily weakening host restrictions. Security middleware, startup, setup, login, redirects, and DNS operations now use explicit error-returning configuration reads and fail closed with `503` or abort startup.

### High

3. **Stored Argon2 parameters were unbounded.** Corrupted or attacker-modified hashes could request excessive memory or CPU. Version, iterations, memory, parallelism, salt, and output sizes are bounded before derivation.
4. **Setup could permanently lock out the administrator.** Setup previously permitted completion without a validated domain and then enabled direct-IP blocking. A valid, verified primary domain is now mandatory, and setup remains resumable until TLS/nginx provisioning completes.
5. **Encrypted-secret migration could destroy the only usable legacy key.** A failed rewrite could still remove the old key. Migration is now transactional, fail-closed, and tested with a single SQLite connection and forced failures.
6. **Managed subdomain deletion could leave false active state after DNS removal.** Links are paused before external cleanup, restored when Cloudflare cleanup fails, and left explicitly paused when DNS succeeded but local deletion failed. Concurrent link/domain mutations are serialized.
7. **Release inputs were not reproducible.** The frontend lockfile referenced 205 temporary local tarballs; Go checksum/vendor evidence was incomplete or inconsistent. Registry resolutions, module metadata, and vendor metadata were normalized. Clean frontend installation now succeeds.
8. **The original release archive lacked a trustworthy binary/manifest path.** It had no official Go 1.26.4 binary, no valid complete installer manifest, and stale source evidence. The tree now contains deterministic packaging, exact-toolchain checks, checksum generation, release attestations, and a tag-triggered CI workflow.

### Medium

9. **Successful password unlocks bypassed expiry/click-limit rechecks and were not counted.** Unlock now validates the current link state atomically and records exactly one accepted human redirect.
10. **Password unlock accepted credentials from query parameters and weak request parsing.** It now requires a small URL-encoded POST body, rejects unexpected media types, and never reads the password from the URL.
11. **Same-origin validation ignored ports.** Scheme, normalized hostname, and effective port are now compared, including correct handling of default ports.
12. **Changing the default link domain could silently move the admin origin.** The administrator UI remains pinned to the primary installation domain; the default domain affects only newly created links.
13. **Cloudflare DNS verification during initial setup could accept a primary hostname pointing elsewhere.** Initial setup now requires the primary record to resolve to the configured public address and rejects unverifiable indirection.
14. **Cloudflare caches were shared across API tokens.** Cache keys now include token identity, preventing authorization/state leakage between credentials.
15. **SSRF-sensitive setup checks inherited environment proxies.** Those checks now use a dedicated client without environment proxy routing, no redirect following, strict public-address validation, and bounded timeouts/body reads.
16. **Database/storage failures were repeatedly disguised as `404`, `401`, `409`, or ordinary conflicts.** Login, redirects, alias checks, links, QR generation, domains, setup, and migration paths now distinguish missing rows, uniqueness conflicts, Cloudflare failures, and storage outages.
17. **Several SQLite commit/constraint paths could leave misleading state.** Transaction commit failures, row-count checks, scan/close errors, and typed uniqueness handling are now explicit; broken connections are not treated as successful operations.
18. **Equal text in separate fields could receive the wrong validation limit.** Field validation no longer uses a value-keyed map that collapses identical values such as `tag` and `notes`.
19. **Conflicting expiry/update inputs and exhausted-link reactivation were ambiguous.** The API rejects contradictory clear/set inputs and requires the limiting condition to be cleared before reactivation.
20. **Frontend mutations could lie about success.** Link/domain deletion, verification, QR loading, and authentication-outage paths now preserve state and show server failures instead of silently removing or replacing UI data.
21. **The Links page silently hid data after 200 rows and lacked the advertised expired filter.** Incremental loading and the expired filter are implemented, and totals are no longer represented as complete when only a page was loaded.
22. **Setup status could reuse stale unauthenticated state immediately after bootstrap login.** Cache/state invalidation now reflects the authenticated transition.
23. **Installer legacy IPinfo cleanup contained malformed embedded Python.** The migration block is executable again and is checked by a source validator.
24. **Installer manifest verification proved only listed-file integrity, not archive completeness.** Installation rejects unlisted regular files, symlinks, FIFOs/devices/sockets, missing sensitive files, and modified files.
25. **JSON response encoding could commit a success status before serialization failed.** Responses are encoded before headers/status are written.
26. **Analytics/report and migration reliability defects were corrected.** Cache scoping, detailed-event rollback, retained totals, country-source migration errors, GPC/DNT aggregate-only behavior, and single-connection reporting have regression coverage.

### Low and hardening

27. Privileged-helper socket/path checks, fixed backend-port validation, proxy-secret validation, nginx host patterns, command allowlists, and rollback behavior were tightened.
28. The release race target now runs CGO-heavy packages sequentially to avoid an observed all-package runner stall while preserving race coverage.
29. Historical audit/status documents are marked as non-authoritative for a newly produced artifact.

## Validation completed

| Validation | Result |
|---|---|
| Clean `npm ci --include=optional` | Pass — 139 packages audited |
| `npm audit --audit-level=high` | Pass — 0 reported vulnerabilities |
| Frontend TypeScript check | Pass |
| Frontend production build | Pass — output embedded in `backend/web` |
| Source/script validator | Pass — 10 embedded Python blocks and 5 shell scripts |
| `go test ./...` | Pass |
| `go vet ./...` | Pass |
| Race detector, main package | Pass |
| Race detector, SQLite/QR packages | Pass |
| Installer/source checksum verification after regeneration | Pass |
| Regression tests for the findings above | Pass |
| Dangerous frontend HTML/eval API scan | No uses found |
| Default HTTP client / TLS bypass scan | No `InsecureSkipVerify` or unbounded default request helpers found in application code |

Local validation environment:

- Go `1.23.2` with `GOTOOLCHAIN=local` for source tests only
- Node.js `22.16.0`
- npm `10.9.2`

## Items that still require the repository owner or production environment

1. **Official build:** produce the release in clean CI using the exact `toolchain go1.26.4` pin. A local Go 1.23.2 test build must not be called official.
2. **Publisher identity:** replace `YOUR_USERNAME` in the README with the real GitHub owner/repository.
3. **Signing/provenance:** push the intended annotated/verified release tag and require the included workflow to publish checksums and GitHub attestations.
4. **Live integration:** perform a staging deployment against real Cloudflare DNS, nginx, certbot, systemd socket activation, firewall rules, and certificate renewal. Those external systems cannot be faithfully exercised in this container.
5. **Additional scanners:** run `govulncheck`, `gosec`, and `staticcheck` in connected CI. They were not installed in this environment. The live Go proxy verification command also could not complete because this container could not resolve `proxy.golang.org`; vendored compilation and tests passed.
6. **Independent assurance:** this is a source-assisted audit, not a formal third-party penetration test, compliance certification, or proof that no unknown bug exists.

## Publication decision

Do not publish the original upload or any old prebuilt binary. Publish only the archive and binary produced from this corrected source by a successful Go 1.26.4 clean-CI run, after checking the release URL identity, SHA-256 file, build metadata, and provenance attestation.
