# ADB Integration Progress

This tracker is the implementation-order gate for the ADB integration series. It was reconstructed on `agent/adb-support` from issues #1-#10 because the issues referenced this file before it was committed.

| Issue | Scope | Status | Evidence |
|---|---|---|---|
| #1 | Managed ADB runtime foundation | Complete | CI run 11: `make test`, binary build/start, amd64 + arm64 container builds, packaged `adb version`; VERSION unchanged. |
| #2 | Registry, authorization, pairing, connection APIs | Pending | |
| #3 | PWA ADB setup and connection status | Pending | |
| #4 | Device information and package/launcher inventory | Pending | |
| #5 | PWA app discovery and launcher import | Pending | |
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
