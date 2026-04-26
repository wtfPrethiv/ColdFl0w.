# Coldflow

> [!NOTE]
> Still under development.

Persistent academic outreach agents in Go, with an ASCII dashboard over SSH.

Eight named bots share a durable research workflow while retaining separate chats and editable memories. They discover professors, match papers, extract source quotations, write personalized emails, deliver through Gmail, watch replies, and track outcomes in Notion. Discord and WhatsApp provide alerts and private bot chat.

## The agents

An agent is a named bot with a specific job, its own chat history, and editable memory. The agents pass work to each other through a shared queue. They run as lightweight roles inside one Go service, with a hosted AI API handling model requests.

| Agent | What it does |
|---|---|
| **Coordinator** | Organizes campaign work and tracks progress across the other bots. |
| **Scout** | Searches public faculty and lab pages for relevant professors, affiliations, and published email addresses. |
| **Matcher** | Finds papers by the professor and ranks them against your research interests. |
| **Analyst** | Extracts useful findings and checks that the quoted passages exist in the accessible paper text. |
| **Composer** | Turns your background, the paper findings, and your template into a personalized email or follow-up. |
| **Courier** | Checks approval, attachments, sending limits, and reply status before sending through Gmail. |
| **Inbox** | Watches for replies, stops pending follow-ups, and helps classify declines, bounces, and meeting discussions. |
| **Reporter** | Prepares daily summaries, delivers reminders and notifications, and synchronizes outreach records with Notion. |

You can chat with each bot in the dashboard. For example, tell Scout `remember Focus on professors working on efficient inference` to save a preference for future work. Memories can be viewed, edited, or forgotten. Shared profile information gives all bots the same background about you; each bot also keeps its own conversation and preferences.

## How the workflow works

```text
Your profile + research interests + campaign rules
                       |
                       v
Scout -> Matcher -> Analyst -> Composer -> Courier -> Gmail
                                                         |
                                                         v
                                            Inbox -> Reporter
                                                      |  |  |
                                           Discord / WhatsApp / Notion
```

1. **Describe your goal.** Supply your research interests, relevant experience, what you are asking professors for, and any location or institution preferences. A campaign groups these choices with an email template and authorized attachments.
2. **Find a suitable professor and paper.** Scout uses supplied URLs or web search to find public contact information. Matcher looks for papers with matching authorship and affiliation, then selects one related to your interests. Existing contacts are deduplicated across campaigns.
3. **Build the research connection.** Analyst extracts source-backed talking points. Composer uses those findings and your profile to explain why you are contacting that professor. Source links and supporting excerpts stay with the draft so you can inspect them.
4. **Review or automate delivery.** In approval mode, you browse drafts and approve the final email and attachments. In automatic mode, eligible drafts proceed under the campaign's rules. Abstract-only evidence and edited drafts still require manual review; missing evidence or failed factual checks block automatic sending.
5. **Track what happens next.** Inbox monitors Gmail while the service runs. A reply stops pending follow-ups immediately, before AI classification. Reporter sends alerts, maintains reminders, produces the daily digest, and updates Notion.

The workflow, drafts, conversations, and memories are saved in SQLite. Jobs survive service restarts. If a send is interrupted and Gmail may have accepted it, Coldflow checks the Sent folder before considering another attempt; an unresolved result is held for review.

## How SSH and the dashboard work

SSH gives you a terminal on the VPS. Coldflow runs there, while your laptop displays the dashboard and sends your keyboard input. The dashboard lets you inspect bots, chat, review drafts, read replies, edit memories, and manage campaigns through the same terminal connection.

```text
Your laptop -- SSH --> VPS
                       |
                       +-- Background service: agents, inbox, schedules
                       +-- TUI: the dashboard you open and close
                       +-- SQLite: saved work, conversations, and memories
```

For continuous operation, the VPS runs `coldflow serve` as a background service using systemd. After connecting over normal SSH, you open `coldflow tui` to attach the dashboard. Closing that dashboard or disconnecting SSH leaves the background service working. Starting the service under systemd is what keeps it independent of your SSH session.

There is also an optional **Wish SSH server**: connect to its configured port with your allowlisted SSH key and it opens the dashboard directly. Normal SSH remains available for administering the VPS. Both dashboard routes use the same running service and saved state.

On a Windows or macOS laptop, the service and TUI can run locally instead. Monitoring runs while that computer is awake, online, and running the service. A VPS is the intended host for 24/7 operation. Discord and WhatsApp alerts reach you without an open SSH session; their inbound chat uses separate HTTPS webhook endpoints.

## Features

- **Research-backed personalization:** OpenAlex paper matching, supplied URLs or Brave Search, public-page extraction, source quotations, and optional PDF text extraction.
- **Draft and attachment control:** an editable default template, a reusable attachment library, approval and automatic modes, and draft revisions that clear previous approval when edited.
- **Outreach scheduling:** sending windows, daily caps, spacing between emails, one follow-up after seven days, and immediate follow-up cancellation on replies.
- **Inbox and relationship tracking:** reply classification, declines, bounces, meeting discussions, manual corrections, and reminder dates.
- **Discord and WhatsApp:** notifications, bot chat, status queries, memory corrections, and pause/resume commands for the configured operator.
- **Notion integration:** contact and outreach records, with selected two-way updates for status, notes, and next-action dates. Sending approval stays in Coldflow.
- **Persistent bot context:** separate chat histories and editable memories, shared profile information, natural-language activity logs, daily summaries, and visible failed jobs.
- **Small-VPS design:** one Go service, SQLite, bounded work and caches, token budgets, hosted AI providers, backup/restore, and no local model requirement.
- **Charm terminal interface:** Bubble Tea, Bubbles, Lip Gloss, Huh, Glamour, Log, Fantasy, Wish, and lightweight Harmonica feedback; ASCII chrome with color and reduced-motion options.

## Defaults and limits

One operator, one Gmail account, multiple campaigns. Defaults: Asia/Kolkata; weekdays 09:00–17:00; 10 new emails and 10 follow-ups per day globally, with additional campaign caps; at least five minutes between deliveries. Enter queues mail for the next eligible sending time. Polling defaults to 60 seconds; Pub/Sub can wake it sooner. Alerts follow provider/network latency rather than a guaranteed real-time SLA.

Research is HTTP-based: JavaScript-only directories and inaccessible papers may need manually supplied sources. OpenAlex author **and institution** matching is conservative; unmatched papers can be supplied through Contacts (`t`). Abstract-only findings are labeled and require manual approval in automatic campaigns. Factual review uses both exact source checks and AI assessment; inspect drafts and sources before relying on them.

The application does not book calendar meetings, run browser automation, serve a web dashboard, or manage multiple users. Chat can discuss actual state and save explicit preferences; workflow edits use dashboard controls. Credentials, provider accounts, search/AI usage, WhatsApp account/template approval, domain, and TLS are operator-provided.

## Documentation

- [Account setup](docs/SETUP.md): Gmail, AI, research, Discord, WhatsApp, Notion, and configuration reference.
- [VPS / Windows / macOS operation](docs/DEPLOYMENT.md): background services, HTTPS, SSH, backups, upgrades, and laptop recovery.
- [Architecture and API](docs/ARCHITECTURE.md): agent roles, persistence, interfaces, and failure handling.
- [Validation record](docs/VALIDATION.md): checks performed and live-account/VM checks still required.

## Installation and usage

### Try it without credentials (i hope u don't, tui is just straight up ass)

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

### Connect your accounts

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

### Open the dashboard over SSH

Once the VPS service is running, connect from your computer:

```sh
ssh YOUR_USER@YOUR_VPS
```

Then open the dashboard using the service's data directory. For the supplied systemd setup:

```sh
sudo -u coldflow /usr/local/bin/coldflow --data /var/lib/coldflow tui
```

For the optional Wish server, after configuring its port and authorized public keys:

```sh
ssh -p 23234 -t coldflow@YOUR_VPS
```

See the deployment guide for the service, firewall, and key setup. The local demo is a different lifecycle: quitting its dashboard also stops its temporary service.

### Development and release builds

```sh
go test ./...
go test -race ./...
go vet ./...
sh scripts/build.sh
```

Windows release builds: `powershell -File scripts/build.ps1`. Artifacts go to `dist/`; the repository may also have a locally built binary in `bin/`. Binaries need no Go installation or system SQLite. The target is a 1-vCPU/1-GB VM with application RSS below 200 MiB idle / 450 MiB under bounded research. These are engineering targets; actual Linux VM soak and live-provider verification must be measured for your deployment.
