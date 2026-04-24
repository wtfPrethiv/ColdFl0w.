# Validation record

Built and checked locally on Windows amd64 with Go 1.27.1. No real mail, Discord/WhatsApp messages, or Notion changes were sent during verification.

## Completed checks

- `go test ./...`: storage restart/queue behavior, source filtering and robots rules, MIME/header handling, structured bounce parsing, draft navigation, stale/duplicate approvals, evidence rejection, research-to-draft workflow, automatic-mode abstract review, reply cancellation, ambiguous-send reconciliation, webhook signatures/replay rejection, webhook/memory idempotence, daily caps, changed attachments, API authentication, incremental Notion sync/stop imports, Retry-After, and backup/restore/path traversal.
- `go test -race ./...`: concurrent approvals and all package tests under the race detector.
- `go vet ./...` and `git diff --check`.
- Five pure-Go release targets: Linux amd64/arm64, macOS amd64/arm64, Windows amd64. Build scripts emit SHA256SUMS alongside artifacts.
- Windows process smoke: initialize isolated demo, start HTTP daemon, load all eight bots/three drafts, approve a simulated email, save a bot memory, forcibly stop/restart the process, verify persisted delivery/memory, then sample idle resident memory. Short runs observed 30.5–32 MiB peak RSS. This is a Windows demo measurement, not a live Linux workload guarantee.
- Interactive Windows PTY check: opened the real ASCII demo, switched to Drafts, and exercised keyboard navigation/delivery; terminal views are also covered by model tests.

## Operator checks still required

- Complete OAuth and use a controlled test recipient to verify actual Gmail identity, threading, attachments, token renewal, bounce handling, and reply synchronization.
- Verify real model compatibility, search/OpenAlex credentials, website accessibility, PDF extraction, and research quality for your subject area.
- Register real Discord/WhatsApp callbacks; verify provider signatures, account allowlists, notification templates, conversation-window behavior, and delivery failure visibility.
- Share a Notion data source with the documented schema; verify selected-field imports, simultaneous edits, and stop-status priority with the actual workspace.
- Measure peak RSS/CPU on the target 1-vCPU/1-GB Linux VM with bounded real research and attachment workloads. Then run the supplied 24-hour isolated demo soak and test sleep/reconnect/reboot recovery.
- Native UI/account tests on macOS/Linux/Windows terminal hosts; cross-compilation alone does not establish native runtime behavior on every platform.

The CI workflow repeats tests, race checks, and vet on Linux, Windows, and macOS when the repository is pushed. This workspace has no configured Git remote; CI has not been run remotely.
