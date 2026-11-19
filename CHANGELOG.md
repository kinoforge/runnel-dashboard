# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/). Before 1.0, minor versions may change the config.

## [Unreleased]

## [0.0.3-beta.3] - 2026-11-19

### Added

- `login.user_max_attempts` limits failed logins for one user name from all addresses. It is off by default.

### Fixed

- Failed logins no longer block a user name everywhere by default. Before, anyone could send five wrong passwords for `admin` and lock it out from every address for the window.
- An oversized user name on a failed login is no longer stored in the limiter or logged in full.
- With `client_ip_header`, the client address comes from the last header line, the one the proxy added, so a client cannot choose its own address with an extra header line.
- A pool paused for a while with "pause for" shows Resume, and can be resumed early.

## [0.0.3-beta.2] - 2026-11-14

### Added

- Settings pages for queues and pools: start rate, burst, and pause a pool for a while. Saving sends a merge patch with `If-Match`, so a newer change is never overwritten.
- Pause works on queues that have no settings yet.

### Changed

- Pause and resume send one `PATCH` of `paused` instead of reading and writing back the whole setting, so an app that sets pool slots at the same time no longer undoes a pause.
- Needs Runnel 0.0.3-beta.2 or newer.

## [0.0.3-beta.1] - 2026-11-14

### Added

- Web dashboard for one or more Runnel servers: instances, namespaces, queues, pools with slots in use, schedules, events, keys, jobs, memory and storage.
- Job cancel and retry, and queue and pool pause and resume.
- Login with bcrypt users, sessions, CSRF protection, a login limiter and an audit log.
- Each Runnel instance is one connection URL, such as `https://admin:${PASSWORD}@host:8700?ca=ca.pem`, with optional client certificates.
- Images at `ghcr.io/kinoforge/runnel-dashboard`. Needs Runnel 0.0.3-beta.1 or newer.

[Unreleased]: https://github.com/kinoforge/runnel-dashboard/compare/v0.0.3-beta.3...HEAD
[0.0.3-beta.3]: https://github.com/kinoforge/runnel-dashboard/compare/v0.0.3-beta.2...v0.0.3-beta.3
[0.0.3-beta.2]: https://github.com/kinoforge/runnel-dashboard/compare/v0.0.3-beta.1...v0.0.3-beta.2
[0.0.3-beta.1]: https://github.com/kinoforge/runnel-dashboard/releases/tag/v0.0.3-beta.1
