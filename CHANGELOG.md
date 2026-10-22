# Changelog

All notable changes to this project are documented in this file. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-10-22

### Added

- Initial release.
- `tenantdb` runs GORM queries in a Postgres transaction that sets the tenant first, so
  row-level security policies filter the rows. It refuses a role those policies do not apply to.
- `grpctenant` opens that tenant scope for each gRPC call.
- `testdb` runs DAO tests against your real schema.

[Unreleased]: https://github.com/tetriz-ai/pgtenant/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/tetriz-ai/pgtenant/releases/tag/v0.1.0
