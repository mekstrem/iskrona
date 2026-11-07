# Changelog

## v0.3.1 — 2026-11-07

### Changed

- Profit examples always include one unit; Details shows trade costs for an adjustable quantity, with quantity-entry help. (#19, #20, #29, #30)
- Added the `fees.buy_location` setting, defaulting to `structure`; use `npc` for NPC buy orders. (#21)
- Added ISK donation information to the README. (#32)

### Fixed

- Structure buy-order estimates now include the 0.5% SCC surcharge and a 100 ISK broker minimum per order. Dashboard margins estimate a one-unit order. (#21)
- Automatically copied prices now match the dashboard; changing the distance filter also copies a newly selected price when available. (#27, #31)
- Market folder scanning reports errors and resumes after recovery. (#22)
- Concurrent settings updates no longer overwrite each other; failed login settings saves restore previous credentials where possible. (#23, #24)
- EVE login reports saved-authorization errors instead of unnecessarily reopening browser authorization, and the callback page directs users to the terminal for the final result. (#25, #28)

## v0.3.0 — 2026-11-02

### Added

- Market Relay checks for newer stable GitHub releases in the background at startup and shows the available version and releases-page URL. Checks time out after three seconds, silently ignore failures, and skip source builds reporting `dev`. Updates must be downloaded and installed manually. (#15)

### Changed

- Reorganized keyboard help into two columns grouped by copy actions, views, distance, and app controls. Help clarifies that shortcuts require closing the help sheet and distinguishes closing a sheet from quitting the app. (#16)

## v0.2.0 — 2026-10-30

- Initial public release.
