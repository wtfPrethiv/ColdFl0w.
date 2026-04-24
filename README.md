# Coldflow

Persistent academic outreach agents in Go, with an ASCII dashboard over SSH.

```text
+--------------------------------------------------------------------------+
| COLDFLOW / LIVE                                                          |
| [Overview] Agents Campaigns Drafts Inbox Contacts Memory Settings Jobs    |
|                                                                          |
| [.] Coordinator  Plans campaigns         [*] Scout     Checking lab pages |
| [.] Matcher      Finds relevant papers   [.] Analyst   Verifies evidence  |
| [.] Composer     Writes personal drafts  [.] Courier   Awaits approval    |
| [.] Inbox        Watching Gmail          [.] Reporter  Notion connected   |
|                                                                          |
| ACTIVITY                                                                 |
| Scout: Found a published contact address on a university profile.         |
| Analyst: Verified two findings against the accessible paper text.         |
| Composer: Prepared a draft with your authorized CV attachment.            |
+--------------------------------------------------------------------------+
```

Eight named bots share a durable research workflow while retaining separate chats and editable memories. They discover professors, match papers, extract source quotations, write personalized emails, deliver through Gmail, watch replies, and track outcomes in Notion. Discord and WhatsApp provide alerts and private bot chat.

## Try it without credentials

Requires Go 1.27.1 or newer to build. Binaries need no Go installation or system SQLite.

```sh
go build -trimpath -o coldflow ./cmd/coldflow
./coldflow demo
```

On Windows:

```powershell
go build -trimpath -o coldflow.exe ./cmd/coldflow
.\coldflow.exe demo
```

Demo uses a separate `demo` subdirectory, fictional professors/papers, and simulated delivery. No provider or email calls occur. Exit with `q`; demo's service stops with its dashboard. Normal `serve` runs independently.

## Connect your accounts

```sh
./coldflow init
./coldflow configure
# Set credentials as described in docs/SETUP.md.
./coldflow auth gmail
./coldflow doctor
./coldflow serve
```

In another terminal or SSH session:

```sh
./coldflow tui
```

Use `--data DIRECTORY` with **every** command when choosing a custom installation directory. The default is the OS user configuration directory plus `coldflow` (`~/.config/coldflow` on Linux, `%APPDATA%\coldflow` on Windows, `~/Library/Application Support/coldflow` on macOS). Use local disk for the SQLite data directory; keep it outside cloud-sync folders and network shares.

In Settings, edit your profile and import attachments. In Campaigns, create a campaign, paste professor/university seed URLs or enable Brave Search, choose interests, template, sending mode, and authorized attachments.

| Keys | Action |
|---|---|
| Tab / Shift-Tab / 1–9 | Switch views |
| Left / Right | Previous / next draft or record |
| Enter in Drafts | Approve and queue the displayed revision |
| e / r / s in Drafts | Edit / regenerate / skip |
| PgUp / PgDn | Scroll full email, evidence, or activity |
| c or Enter in Agents | Chat with selected bot |
| `remember ...` in chat | Save a preference to that bot's memory |
| n / e / x in Memory | Add / edit / forget a memory |
| p | Pause / resume all sending and discovery |
| q | Detach TUI; background service continues |

Form arrows edit fields rather than navigate drafts. Escape closes a form. Email edits invalidate approval. Automatic campaigns send eligible drafts without pressing Enter; abstract-only evidence and manually edited drafts require review. Missing evidence or failed factual checks blocks automatic delivery. Drafts display their status and review reason.

## What is included

- SQLite WAL, restartable queue, full-text search API, migrations, backups, durable delivery intent and uncertain-send reconciliation.
- OpenAlex paper matching, Brave search or supplied seeds, public-page extraction, robots rules, source links, exact-quote verification, bounded PDF extraction with optional `pdftotext`.
- Configurable OpenAI, Anthropic, OpenRouter, and compatible API endpoints via Charm Fantasy. No local model.
- Gmail OAuth, attachments, threaded follow-ups, inbox history reconciliation, optional Pub/Sub wakeups, and polling fallback.
- One follow-up after seven days. Every reply stops follow-ups immediately, before AI classification. Reply labels and meeting discussions can be corrected manually.
- Discord and official WhatsApp Cloud API notifications/chat; signed webhooks and operator allowlists.
- Notion tracking, selected two-way fields, reminder dates, conflict events, and stop-status priority.
- Daily digest at 20:00 local time, reminders, per-bot memory, queue failures/retries, provider health, and token accounting.
- Bubble Tea, Bubbles, Lip Gloss, Huh, Glamour, Log, Fantasy, Wish, and lightweight Harmonica feedback. ASCII chrome, `NO_COLOR`, and `reduced_motion` support.

## Defaults and limits

One operator, one Gmail account, multiple campaigns. Defaults: Asia/Kolkata; weekdays 09:00–17:00; 10 new emails and 10 follow-ups per day globally, with additional campaign caps; at least five minutes between deliveries. Enter queues mail for the next eligible sending time. Polling defaults to 60 seconds; Pub/Sub can wake it sooner. Alerts follow provider/network latency rather than a guaranteed real-time SLA.

Research is HTTP-based: JavaScript-only directories and inaccessible papers may need manually supplied sources. OpenAlex author **and institution** matching is conservative; unmatched papers can be supplied through Contacts (`t`). Abstract-only findings are labeled and require manual approval in automatic campaigns. Factual review uses both exact source checks and AI assessment; inspect drafts and sources before relying on them.

The application does not book calendar meetings, run browser automation, serve a web dashboard, or manage multiple users. Chat can discuss actual state and save explicit preferences; workflow edits use dashboard controls. Credentials, provider accounts, search/AI usage, WhatsApp account/template approval, domain, and TLS are operator-provided.

## Deployment, development, and verification

- [Account setup](docs/SETUP.md): Gmail, AI, research, Discord, WhatsApp, Notion, and configuration reference.
- [VPS / Windows / macOS operation](docs/DEPLOYMENT.md): systemd, HTTPS, SSH, backups, upgrades, and laptop recovery.
- [Architecture and API](docs/ARCHITECTURE.md): agents, persistence, interfaces, and failure handling.
- [Validation record](docs/VALIDATION.md): checks performed and account/VM checks still required.

```sh
go test ./...
go test -race ./...
go vet ./...
sh scripts/build.sh
```

Windows release builds: `powershell -File scripts/build.ps1`. Artifacts go to `dist/`; Windows convenience binary goes to `bin/` during local builds. SQLite is pure Go. The target is a 1-vCPU/1-GB VM with application RSS below 200 MiB idle / 450 MiB under bounded research. These are engineering targets; actual Linux VM soak and live-provider verification must be measured for your deployment.
