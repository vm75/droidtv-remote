# ADB Integration Progress

This tracker is the implementation-order gate for the ADB integration series. It was reconstructed on `agent/adb-support` from issues #1-#10 because the issues referenced this file before it was committed.

| Issue | Scope | Status | Evidence |
|---|---|---|---|
| #1 | Managed ADB runtime foundation | Complete | CI run 11: `make test`, binary build/start, amd64 + arm64 container builds, packaged `adb version`; VERSION unchanged. |
| #2 | Registry, authorization, pairing, connection APIs | Complete | CI run 33: gofmt, `make test`, binary build, disabled-ADB smoke; auth/persistence/multi-TV/REST-MCP tests; completion gate runs amd64 + arm64 containers. |
| #3 | PWA ADB setup and connection status | Complete | CI run 42: full repo/browser tests, binary build, smoke; both ADB setup paths, token/session security, TV switching, auth failures, disconnect/forget covered. Visual/hardware screenshot validation deferred to #10. |
| #4 | Device information and package/launcher inventory | Complete | CI run 59: gofmt, full Go/browser tests, binary build, smoke; parser/API/MCP coverage includes allowlisted device info, current-user package and Leanback inventories, deterministic bounds, noisy/truncated/unsupported/offline behavior, auth, multi-TV serial isolation, and failing REST/MCP parity. |
| #5 | PWA app discovery and launcher import | Complete | CI run 65: full Go/browser tests, binary build, smoke. Browser coverage includes launchable-first/all-installed views, exact-package duplicate handling, editable-name validation, preview/cancel with zero writes, selective import, preserved ordering, empty/error cases, and stale TV-switch isolation. Screenshots deferred to #10. |
| #6 | Secure single-APK install/update backend | Pending | |
| #7 | PWA APK sideload/update workflow | Pending | |
| #8 | Guarded third-party package administration | Pending | |
| #9 | Bounded diagnostics and reboot | Pending | |
| #10 | Final integration audit and permanent documentation | Pending | |

## Rules

- Work in numeric order.
- Do not change `VERSION` in this series.
- Keep Android TV Remote v2 behavior independent from ADB.
- Do not expose arbitrary shell or arbitrary ADB command execution.
- Every device operation must target an explicit stored ADB serial/service identity.
- ADB mutation and diagnostic surfaces must be authenticated, bounded, and no-store.
- Issue #10 deletes this tracker only after issues #1-#9 have completion evidence.
