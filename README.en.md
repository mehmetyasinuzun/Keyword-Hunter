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
- **Built for teams:** admin / analyst / viewer roles, case status and analyst notes, scheduled scans, webhooks (Slack / Discord / Teams / SIEM).
- **Analyst experience:** command palette (<kbd>Ctrl</kbd>+<kbd>K</kbd>), keyboard shortcuts, light/dark/system theme, Turkish + English UI, mobile-friendly.

## ✨ Features

| Module | Capabilities |
|---|---|
| 🔎 **Search** | Parallel search across 10 live-verified onion engines, per-engine live progress (SSE), pagination, automatic severity (1-5) and category (EN + TR signals), dedup |
| 🗂️ **Findings** | Multi-dimensional filters, sorting, bulk actions, inline editing, **case management** (new → investigating → confirmed / dismissed + notes), CSV · JSON · **STIX 2.1** |
| 🧬 **Tagging + IOC** | Fetches the page over Tor; Unicode-aware keyword extraction; email, BTC/XMR, IP, hash, card (Luhn), SSH/API keys, onion IOCs; pivot search |
| 🕸️ **Map** | Query → engine → result relationship graph (radial / tree / force), node expansion (adds on-page links), focus, add to watchlist |
| 📈 **Analytics** | Time series, source / query / severity / category breakdowns, domain map, theme-aware charts |
| ⏱️ **Scheduled scans** | Minute-level intervals, diffing for *new* URLs only, per-scan and global webhooks, thresholds, test send |
| 📡 **Watchlist** | Uptime %, change detection via visible-text hash, Cloudflare/captcha detection, triggered and manual screenshots, timeline |
| 🛰️ **Engines** | Live health, latency, success rate, enable/disable (actually affects search), automatic Tor circuit renewal (NEWNYM) |
| 🕷️ **Crawler** | Polite, depth/page-bounded BFS from a seed .onion; discovered pages become findings |
| 🍪 **Site profiles** | Per-host Cookie + User-Agent + JS rendering (behind login walls / captchas) |

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
- RBAC (admin / analyst / viewer) enforced server-side; navigation filtered server-side.
- CSRF on every write endpoint (including the search form); Fetch-Metadata-protected `GET /logout`.
- SSRF guards: expand / analyze / watch / render are `.onion`-only; webhook and screenshot targets cannot resolve to internal addresses; redirects are not followed.
- Secrets masked in logs, API and HTML; CSV export protected against formula injection.
- All assets embedded (no CDN), `govulncheck` on every push, Dependabot, SHA256 sums on releases; Docker runs as non-root with `no-new-privileges`.

Report vulnerabilities privately — see [SECURITY.md](SECURITY.md).

## 🧑‍💻 Development

```bash
go build ./... && go vet ./... && go test -race ./...
```

Frontend contract: [docs/FRONTEND.md](docs/FRONTEND.md) · Contributing: [CONTRIBUTING.md](CONTRIBUTING.md) · Changelog: [CHANGELOG.md](CHANGELOG.md).

## ⚖️ Legal

For security researchers and SOC/CTI teams only. Use in an **isolated environment** and **within your legal authority**. Not for unauthorized access or illegal activity; the user bears all responsibility.

<div align="center"><sub>Built by <a href="https://github.com/mehmetyasinuzun">Mehmet Yasin Uzun</a> · MIT License</sub></div>
