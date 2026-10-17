<div align="center">

<picture>
  <source media="(prefers-color-scheme: light)" srcset="docs/assets/banner-light.svg">
  <img alt="KeywordHunter — Dark Web Cyber Threat Intelligence Platform" src="docs/assets/banner-dark.svg" width="100%">
</picture>

[![CI](https://github.com/mehmetyasinuzun/Keyword-Hunter/actions/workflows/ci.yml/badge.svg)](https://github.com/mehmetyasinuzun/Keyword-Hunter/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/mehmetyasinuzun/Keyword-Hunter?display_name=tag&sort=semver&color=6366f1)](https://github.com/mehmetyasinuzun/Keyword-Hunter/releases)
[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-22d3ee.svg)](LICENSE)
[![CSP](https://img.shields.io/badge/CSP-script--src%20'self'-34d399)](docs/FRONTEND.md)

**A single-binary CTI platform that searches onion search engines concurrently over Tor,
classifies findings automatically, extracts IOCs, monitors sites and alerts on new threats.**

[Features](#-features) · [Quick start](#-quick-start) · [Engines](#️-search-engines) · [Screenshots](#️-screenshots) · [Security](#️-security-model) · [**Türkçe**](README.md)

</div>

---

<p align="center">
  <img src="docs/screenshots/dashboard-dark.jpg" alt="Operations dashboard — dark theme" width="49%">
  <img src="docs/screenshots/dashboard-light.jpg" alt="Operations dashboard — light theme" width="49%">
</p>

## 🎯 Why KeywordHunter?

- **One console, the whole loop:** search → classify → extract IOCs → watch → alert → export as STIX.
- **Tor-only, local-first:** all outbound traffic goes through Tor; data lives only in your SQLite file. No CDN, no telemetry.
- **Single binary, zero runtime deps:** pure Go (no CGO), six platforms, ready-made Docker image.
- **Built for teams:** admin / analyst / viewer roles, case status and analyst notes, scheduled scans, webhooks (Slack / Discord / Teams / SIEM), personal API tokens for SOAR/SIEM automation.
- **Analyst experience:** command palette (<kbd>Ctrl</kbd>+<kbd>K</kbd>), keyboard shortcuts, light/dark/system theme, Turkish + English UI, mobile-friendly.

## ✨ Features

| Module | Capabilities |
|---|---|
| 🔎 **Search** | Parallel search across 10 live-verified onion engines, per-engine live progress (SSE), pagination, automatic severity (1-5) and category (EN · TR · RU · DE · ES · PT signals; accent/case-insensitive, word-boundary stem matching), dedup |
| 🗂️ **Findings** | Multi-dimensional filters, sorting, bulk actions, inline editing, **case management** (new → investigating → confirmed / dismissed + notes), CSV · JSON · **STIX 2.1** |
| 🧬 **Tagging + IOC** | Fetches the page over Tor; Unicode-aware keyword extraction; email, BTC/XMR, IP, hash, card (Luhn), SSH/API keys, onion IOCs; pivot search |
| 🕸️ **Map** | Query → engine → result relationship graph (radial / tree / force), node expansion (adds on-page links), focus, add to watchlist |
| 📈 **Analytics** | Time series, source / query / severity / category breakdowns, domain map, theme-aware charts |
| ⏱️ **Scheduled scans** | Minute-level intervals, diffing for *new* URLs only, per-scan and global webhooks, thresholds, test send |
| 📡 **Watchlist** | Uptime %, change detection via visible-text hash, Cloudflare/captcha detection, triggered and manual screenshots, timeline |
| 🛰️ **Engines** | Live health, latency, success rate, enable/disable (actually affects search), automatic Tor circuit renewal (NEWNYM) |
| 🕷️ **Crawler** | Polite, depth/page-bounded BFS from a seed .onion; discovered pages become findings |
| 🍪 **Site profiles** | Per-host Cookie + User-Agent + JS rendering (behind login walls / captchas) |
| 🔗 **Integrations** | Read-only **TAXII 2.1 server** for OpenCTI / Microsoft Sentinel (fixed collections, stable STIX objects, token via Basic/Bearer); push selected findings to **MISP** as a single event (encrypted key, SSRF-guarded, optional auto-push) — [docs/INTEGRATIONS.md](docs/INTEGRATIONS.md) |

## 🚀 Quick start

```bash
git clone https://github.com/mehmetyasinuzun/Keyword-Hunter.git
cd Keyword-Hunter
docker compose up -d --build
docker compose logs app | grep -A3 "yönetici parolası"   # admin password generated on first run
```

Open **http://localhost:8080** · health: `/healthz` · prebuilt image: `ghcr.io/mehmetyasinuzun/keyword-hunter:latest`.

Local run (Go 1.27+): `./run.sh` (macOS/Linux) · `.\run.ps1` (Windows) · `./run.sh release` builds six platform binaries into `dist/`.
Tor must be listening (Tor Browser 9150 / tor service 9050) — set `TOR_PROXY` in `.env`.
See the [Turkish README](README.md) for the full configuration table (`.env` variables).

## 🛰️ Search engines

Verified on 2026-09-18 over a real Tor circuit, in three rounds with two queries
([`pkg/search/engines.go`](pkg/search/engines.go)). Enabled by default: **Tordex, Amnesia, Tor66,
Onionway, OnionLand, Torland, Excavator, TorNet, Submarine, Danex**. Engines that require a captcha
(OSS, Torgle), JavaScript (Torgol), have an empty index (DeepSearches) or are unreachable (Torch,
Ahmia onion, FindTor) ship disabled and can be enabled from the **Search Engines** screen. User
customisations survive engine list updates.

## 🖼️ Screenshots

| | |
|---|---|
| <img src="docs/screenshots/login.jpg" alt="Sign in"> **Sign in** | <img src="docs/screenshots/search.jpg" alt="Search"> **Search** |
| <img src="docs/screenshots/results.jpg" alt="Findings"> **Findings** | <img src="docs/screenshots/graph.jpg" alt="Map"> **Map** |
| <img src="docs/screenshots/analytics.jpg" alt="Analytics"> **Analytics** | <img src="docs/screenshots/watchlist.jpg" alt="Watchlist"> **Watchlist** |
| <img src="docs/screenshots/monitor.jpg" alt="Engines"> **Engines** | <img src="docs/screenshots/scheduled.jpg" alt="Scheduled scans"> **Scheduled scans** |
| <img src="docs/screenshots/palette.jpg" alt="Command palette"> **Command palette** | <img src="docs/screenshots/dashboard-light.jpg" alt="Light theme"> **Light theme** |

## 🛡️ Security model

The tool visits hostile sites and displays their text, so the UI assumes **untrusted input** everywhere.

- **Strict CSP** — `script-src 'self'`: no inline scripts or `on*=` handlers anywhere, enforced by an automated test over every template and JS file. Even if dark-web text slipped into the HTML, the browser would not execute it.
- bcrypt, per-IP and global login lockout, sliding + absolute session lifetime, session revocation on password change.
- Two-factor authentication (TOTP, RFC 6238) with one-time recovery codes and optional enforcement — see [Two-factor authentication](#-two-factor-authentication-2fa).
- RBAC (admin / analyst / viewer) enforced server-side; navigation filtered server-side.
- Tamper-evident audit log: every state-changing action (sign-ins, denied access, user/settings changes, deletions, exports) is recorded as "who did what, when, from where" in an append-only, SHA-256 hash-chained table; the admin-only `/audit` page shows where the chain breaks if a row is altered, deleted or truncated. Secrets (passwords, cookies, tokens, webhook paths) are never stored; retention via `AUDIT_RETENTION_DAYS` (default 365, `0` = forever) keeps the chain verifiable through an anchor.
- CSRF on every write endpoint (including the search form); Fetch-Metadata-protected `GET /logout`.
- Personal API tokens: `kh_` + 256 random bits, stored only as a SHA-256 hash and shown once; read/write scope can never exceed the owner's current role; account and security management endpoints refuse tokens (see [API](#-api)).
- SSRF guards: expand / analyze / watch / render are `.onion`-only; webhook and screenshot targets cannot resolve to internal addresses; redirects are not followed.
- Secrets masked in logs, API and HTML; CSV export protected against formula injection.
- Integrations: the MISP API key is stored AES-256-GCM-encrypted with a purpose-separated key derived from `APP_SECRET`, never appears in any response, page or audit row, and must be re-entered when the URL moves to another server; MISP egress is direct clearnet/intranet, not Tor (public targets only unless "allow private networks" is on, no redirects, 1 MB / 15 s caps). TAXII accepts API tokens only (no cookies, no writes); card and phone numbers are shared neither over TAXII nor to MISP; plain-http MISP is allowed only to internal IPs. Details: [docs/INTEGRATIONS.md](docs/INTEGRATIONS.md).
- All assets embedded (no CDN), `govulncheck` on every push, Dependabot, SHA256 sums on releases; Docker runs as non-root with `no-new-privileges`.

Report vulnerabilities privately — see [SECURITY.md](SECURITY.md).

## 🔌 API

Every `/api/` endpoint accepts either a browser session (cookie + `X-CSRF-Token` header on writes) or a **personal API token** — e.g. `GET /api/results?q=&minCriticality=&tag=&limit=&page=`, `GET /api/export/results?format=csv|json`, `GET /api/export/stix`, `GET /api/artifacts?type=&value=`, `POST /api/results/:id/case`, `GET|POST /api/watchlist`, `GET|POST /api/scheduled`, `GET /api/engines`, `GET /api/whoami`, `POST /api/integrations/misp/push`. `GET /healthz` is public.

**Error contract:** every `/api` error returns `{"error": "<human-readable message>", "code": "<stable code>"}` (some endpoints also keep `"success": false` for older clients; the 2FA lockout adds `retryAfter`). Automations should match on **`code`** (`session_expired`, `csrf_failed`, `role_required`, `session_required`, `token_invalid`, `token_revoked`, `token_expired`, `token_read_only`, `2fa_required`, `too_many_requests`, `invalid_request`, `not_found`, `tor_unreachable`, `site_unreachable`, `site_timeout`…); the `error` text is a translation and may change. Message language: the `lang` cookie, otherwise `Accept-Language` (`tr` | `en`, q-values honoured), otherwise Turkish. HTTP status codes do not depend on the language.

```bash
curl -sS -H "Authorization: Bearer kh_wrong" -H 'Accept-Language: en' https://keywordhunter.example/api/results
# 401 {"code":"token_invalid","error":"Invalid API token"}
```

**TAXII 2.1** (read-only, API token only — Bearer or as the Basic password): `GET /taxii2/` (discovery), `GET /taxii2/kh/collections/`, `GET /taxii2/kh/collections/{id}/objects/?added_after=&limit=&next=&match[type]=&match[id]=`, `GET …/objects/{object-id}/`, `GET …/manifest/`. Collections: `findings-all`, `findings-high` (severity 4–5), `indicators`. OpenCTI / Sentinel setup, curl examples and the MISP mapping: [docs/INTEGRATIONS.md](docs/INTEGRATIONS.md).

```bash
curl -sS -H 'Accept: application/taxii+json;version=2.1' -u ":$KH_TOKEN" \
  'https://keywordhunter.example/taxii2/kh/collections/findings-high/objects/?limit=500'
```

### Personal API tokens (SOAR / SIEM / scripts)

Create a token on **My Account → API tokens** (`/account`). The value (`kh_…`) is shown **exactly once**; the server only keeps its SHA-256 hash. Send it in the `Authorization` header of every request:

```bash
export KH_TOKEN='kh_…'   # read it from a vault / secret store
# Read: latest critical findings
curl -sS -H "Authorization: Bearer $KH_TOKEN" \
  'https://keywordhunter.example/api/results?minCriticality=4&limit=20'
# Write ("write" scope): no CSRF header needed when no session cookie is sent
curl -sS -X POST -H "Authorization: Bearer $KH_TOKEN" -H 'Content-Type: application/json' \
  -d '{"status":"investigating","note":"SOAR: triage opened"}' 'https://keywordhunter.example/api/results/42/case'
# Check the token's identity and effective role
curl -sS -H "Authorization: Bearer $KH_TOKEN" https://keywordhunter.example/api/whoami
```

- **Scope:** `read` is downgraded to viewer and can never write; `write` acts with the owner's **current** role (demoting the user demotes the token immediately). Viewers can only create `read` tokens.
- **Lifetime:** 7 / 30 / 90 / 180 / 365 days or no expiry; revocable at any time. Revoked, expired or unknown tokens, and tokens of disabled/deleted users, get `401 {"error": …, "code": "token_invalid|token_revoked|token_expired"}` with `WWW-Authenticate: Bearer`.
- **`/api/` only, header only:** HTML pages, `/events` (SSE) and screenshot files ignore tokens; query-string values such as `?token=` are never read.
- **CSRF rule:** only requests authenticated by a Bearer token *without* a session cookie skip CSRF. If a session cookie is also present, the session path is used and CSRF is required.
- **Endpoints tokens cannot call** (`403 {"code": "session_required", "error": …}`): `/api/tokens*` (token management), `/api/me/*` (password, 2FA), `/api/users*`, `/api/settings/*`, `/api/site-profiles*`, `/api/alert-config*` (webhook integrations), `/api/integrations/misp/config` and `/api/integrations/misp/test` (MISP settings). A leaked token cannot mint tokens, change passwords or manage accounts and integrations.
- **Password changes and incident response:** when an admin resets a user's password, that user's sessions are signed out **and all of their API tokens are revoked** (`tokensRevoked` in the `user.password.reset` event). A user's own password change does not revoke tokens by default (so integrations keep working); if you suspect a compromise, tick "Also revoke all my API tokens" under **My Account → Password** (`"revokeTokens": true` in the `POST /api/me/password` body). Disabling a user also invalidates their tokens immediately.
- **Audit:** token creation/revocation (`token.create`, `token.revoke`), rejected token attempts (`auth.token.failure`) and **every audited action** performed with a token (writes, exports, access denials) are recorded with `auth_method=token` and the token prefix (`kh_XXXXXXX`). Read-only requests (e.g. `GET /api/results`) do not produce their own audit rows; they only update the token's last-used time and IP (at most once a minute), shown in the token list.

## 🔐 Two-factor authentication (2FA)

Enable it under **My Account → Two-factor authentication** (`/account#security-2fa`): add the QR code (or the key, grouped in fours) to an authenticator app (Google/Microsoft Authenticator, Authy, 1Password, Bitwarden, Aegis…), confirm a 6-digit code together with your current password and store the **10 recovery codes** (`xxxx-xxxx`, single use, shown once; copy or download as `.txt`).

- **Sign-in:** a correct password does **not** create a session for a 2FA user. A server-side pending login (5-minute TTL, referenced by an `HttpOnly`, `SameSite=Strict` cookie scoped to `/login`) leads to `/login/2fa`, which auto-submits once 6 digits are typed and offers a recovery-code mode. Five wrong codes invalidate the pending login; code failures also count toward the per-IP login lockout, so restarting with the password cannot bypass it. Attempts are reserved atomically before verification, so concurrent requests cannot exceed the 5-attempt cap. Changing your password also drops any pending login started with the old one. The login page never reveals whether an account has 2FA before the password is verified.
- **TOTP:** HMAC-SHA1, 6 digits, 30 s step, ±1 step clock skew, constant-time comparison, replay protection (the last accepted step is persisted; the same or an older step is rejected).
- **Turning off / regenerating codes** requires the password plus a current code (or a recovery code); failures count toward the IP lockout. Regenerating invalidates all previous codes. **Enabling also requires the current password**, so a stolen session cannot enrol its own authenticator and lock the owner out; enabling 2FA signs out your other sessions.
- **Admin reset** (`/users` → shield icon, `POST /api/users/:id/2fa/reset`) removes a user's enrolment and recovery codes and signs out their sessions (lost device). Admins cannot reset **their own** 2FA through this endpoint — they turn it off on My Account with password + code, so a hijacked admin session cannot strip its own second factor.
- **Enforcement (`REQUIRE_2FA=none|admins|all`, default `none`):** an unenrolled user in scope is redirected to `/account#security-2fa` (with a banner) on every page; session API calls get `403 {"code": "2fa_required", "error": …}` except the enrolment endpoints; that user's API tokens are rejected with 403. The legacy `.env` administrator (`ADMIN_USER`) that is not a row in the users table (e.g. after renaming it in `/settings` or deleting the bootstrap row) cannot enrol, so while a policy is active it **cannot sign in** and any existing session is ended; use an account from `/users`. With `REQUIRE_2FA` set, the app **refuses to start** without a persisted `APP_SECRET` (unwritable `.env`), because enrolment would be disabled and everyone in scope would be stuck on My Account.
- **`APP_SECRET`** (64 hex chars) — TOTP secrets are encrypted with AES-256-GCM using a key derived via HKDF-SHA256 and bound to the user (AAD). If it is missing at startup a new one is generated with `crypto/rand` and appended to the active `.env` as a single line, keeping your comments and other lines (the value is never logged). **Back it up:** if it is lost or changed, stored secrets can no longer be decrypted; users can still sign in with recovery codes, otherwise an admin must reset their 2FA. If the `.env` file is not writable the generated secret only lives for that run and 2FA enrolment is disabled.
- **Endpoints** (interactive session only, never with an API token): `GET /api/me/2fa`, `POST /api/me/2fa/setup` → `{qrDataURI, secret, otpauthURI}`, `POST /api/me/2fa/enable {password, code}` → `{recoveryCodes}`, `POST /api/me/2fa/disable {password, code}`, `POST /api/me/2fa/recovery-codes {password, code}`, `POST /api/users/:id/2fa/reset` (admin).
- **Audit:** `2fa.enable`, `2fa.disable`, `2fa.recovery.regenerate`, `2fa.reset_by_admin`, `auth.2fa.challenge`, `auth.2fa.success`, `auth.2fa.failure` (reason `bad_code`, `replay`, …); `auth.login.success` records the `mfa` method. Secrets and codes are never logged.

## 🧑‍💻 Development

```bash
go build ./... && go vet ./... && go test -race ./...
```

Frontend contract: [docs/FRONTEND.md](docs/FRONTEND.md) · Integrations (TAXII 2.1, MISP): [docs/INTEGRATIONS.md](docs/INTEGRATIONS.md) · Contributing: [CONTRIBUTING.md](CONTRIBUTING.md) · Changelog: [CHANGELOG.md](CHANGELOG.md).

## ⚖️ Legal

For security researchers and SOC/CTI teams only. Use in an **isolated environment** and **within your legal authority**. Not for unauthorized access or illegal activity; the user bears all responsibility.

<div align="center"><sub>Built by <a href="https://github.com/mehmetyasinuzun">Mehmet Yasin Uzun</a> · MIT License</sub></div>
