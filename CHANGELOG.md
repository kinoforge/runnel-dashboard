# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/). Before 1.0, minor versions may change the config.

## [Unreleased]

## [0.0.3-beta.1] - 2026-11-14

### Added

- Web dashboard for one or more Runnel servers: instances, namespaces, queues, pools with slots in use, schedules, events, keys, jobs, memory and storage.
- Job cancel and retry, and queue and pool pause and resume.
- Login with bcrypt users, sessions, CSRF protection, a login limiter and an audit log.
- Each Runnel instance is one connection URL, such as `https://admin:${PASSWORD}@host:8700?ca=ca.pem`, with optional client certificates.
- Images at `ghcr.io/kinoforge/runnel-dashboard`. Needs Runnel 0.0.3-beta.1 or newer.

[Unreleased]: https://github.com/kinoforge/runnel-dashboard/compare/v0.0.3-beta.1...HEAD
[0.0.3-beta.1]: https://github.com/kinoforge/runnel-dashboard/releases/tag/v0.0.3-beta.1
