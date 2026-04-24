# Architecture and interfaces

```text
SSH / local TUI / Wish -------- authenticated local HTTP API
Discord + WhatsApp ----------- signed webhooks -> durable incoming jobs
                                       |
                 SQLite records + FTS + job queue + events
                                       |
                  Coordinator / bounded single worker
                  |        |        |         |
                Scout -> Matcher -> Analyst -> Composer -> Courier
                  |          |         |         |          |
             public web    OpenAlex    source    template   Gmail send
                                       quotes   + profile  + intent
                                       |
                       Inbox monitor / Gmail history
                                       |
                        immediate cancellation of follow-ups
                                       |
                   classification -> reminders -> Reporter
                                       |
                          Notion / Discord / WhatsApp
```

The daemon owns workflow state and secrets. TUI clients are disposable and communicate through a random bearer token stored in the data directory. The external HTTPS proxy publishes only signed webhook routes. One operator and one mailbox are supported; this is not a multi-tenant service.

## Agent model

Agents are named roles within one Go process, with isolated chat/memory records and explicit workflow stages. They are not separate processes or independently running models. Charm Fantasy provides bounded generation through provider adapters; the service performs external side effects. This makes scheduling, budgets, and restarts deterministic enough for a small VM.

Persistent bot states are data, while the current busy/idle indicator is transient. Shared memory supplies profile preferences; per-bot memory supplies role-specific instructions. Only explicit operator `remember` requests or dashboard edits update preferences. Web pages and incoming mail never become authoritative instructions. Chats receive a bounded current-state summary and recent conversation. Long history is capped at 1,000 records, and retrieval/memory prompts are bounded.

## Persistence and delivery

Schema v1 has JSON domain records, an FTS5 index, structured activity events, unique-key jobs, settings/cursors, and a migration table. SQLite has one pooled connection, WAL, busy timeout, and a 4-MiB page-cache target. Large paper text is omitted from list/snapshot reads. Attachments are immutable copied files with hashes; only authorized IDs are selected.

Jobs have queued/running/done/failed states, retry counts, due times, and a ten-minute lease. Work has an eight-minute deadline. A worker claims jobs atomically with `UPDATE ... RETURNING`; deterministic keys make retries idempotent. Five failed attempts require operator retry. Scheduled deferrals do not consume retry attempts. Completed jobs are retained for 14 days, activity for 5,000 events, and research cache for at most 100 files / seven days. Incoming provider event IDs become unique queue keys, so acknowledging a webhook does not lose unprocessed work.

Delivery checks draft revision, current contact suppression/reply state, campaign pause/mode, global pause, sending window, both daily caps, spacing, fresh mailbox history, MIME validity, and file hashes. Before calling Gmail, the service persists `sending`, its stable RFC Message-ID, and an attempt timestamp. A crash/error after that point triggers Sent-folder reconciliation. Unknown outcomes enter `uncertain`; they are never blindly retried. Exactly-once delivery cannot be guaranteed by an external mail API; uncertain outcomes require reconciliation.

Mail monitoring runs independently of AI work. Every incoming tracked reply cancels pending outreach before classification. AI labels are confidence-scored; low-confidence labels remain reviewable. Stops/suppression are deterministic service rules. Follow-ups are one per original email after seven elapsed days and use the same campaign sending policy.

Notion and notifications use durable jobs as their outbox. Notion honors Retry-After, queries remotely changed pages using a watermark, exports at most 20 dirty local contacts per pass, and finds existing pages by Coldflow ID before creating. Selected inbound fields use last-sync metadata; local conflicts produce events, and remote stop-like states take priority. Notification delivery may be repeated if an external provider accepts a request but its response is lost; provider message delivery is not transactional with SQLite.

## Go interfaces

- `ai.Generator.Generate(context, system, prompt)`: bounded, provider-independent generation, used by every role.
- `research.Source`: source fetch, discovery, and verified-author paper matching.
- `mail.Mailer`: delivery, Message-ID reconciliation, inbox changes, and initial history cursor.
- `integrations.Notifier` / `integrations.Notion`: channel delivery and selected-field tracking adapters.
- `domain`: Profile, Campaign, Contact, Paper, Evidence, Draft, Attachment, Memory, Chat, Job, Event, and InboxMessage.

Provider credentials are environment-only except Gmail refresh tokens and local API/SSH keys in owner-private files. Research transport rejects non-public IPs at dial time, limits redirects and ports, checks robots rules, and caps responses. Models have no shell, arbitrary file, or unrestricted action tools.

## Local HTTP API

All `/api/*` routes require `Authorization: Bearer TOKEN`. They are for local trusted clients; no CORS browser access is enabled.

| Route | Purpose |
|---|---|
| GET `/healthz` | Public process liveness |
| GET `/api/snapshot` | Bounded TUI activity plus current campaign/domain records |
| GET `/api/search?q=...` | SQLite FTS query (max 20 matches) |
| POST / PUT `/api/campaign` | Create / edit campaign preferences |
| PUT `/api/profile` | Update user-provided research facts |
| PUT `/api/config` | Persist preferences; service restart applies them |
| POST `/api/attachments?name=...` | Raw file upload into the authorized library |
| POST `/api/action` | Typed workflow action; see `server.Action` |
| POST `/webhooks/discord` | Ed25519-signed, timestamp-checked interactions |
| GET / POST `/webhooks/whatsapp` | Subscription verification / HMAC-verified events |

Action kinds: `approve`, `skip`, `edit-draft`, `regenerate`, `resolve-delivery`, `pause`, `resume`, `campaign`, `contact`, `chat`, `remember`, `edit-memory`, `forget`, `retry`, `discover`, `paper`, `classify`, `inbox-resync`. Approval and editing include the reviewed `revision`; stale revisions return HTTP 400. Repeated approval of the same revision cannot enqueue duplicate sends. Manual source text is abstract/excerpt evidence and never becomes full-text evidence automatically.

## Extension boundaries

Other mail providers implement Mailer; additional discovery providers implement Source. Dynamic browser work, multi-user permission models, local inference, and calendar booking require explicit new designs. Avoid adding heavyweight background services or increasing concurrency without measurements on the target VM.
