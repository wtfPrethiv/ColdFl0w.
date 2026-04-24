# Account setup

Run `coldflow init`, then `coldflow configure`. Preferences live in `config.json` in your chosen data directory. Never put tokens in tracked configuration. On Linux use an environment file readable only by your service user; on laptops set environment variables before launching the background service. The TUI connects to the service and does not need provider credentials itself.

## AI and research

Set `COLDFLOW_AI_KEY`. Choose `ai_provider` (`openai`, `anthropic`, `openrouter`, or `compatible`) and an exact `ai_model` available in your account. For compatible endpoints, set `ai_base_url`, including the provider's API prefix (often `/v1`). Provider-compatible does not imply every model supports every parameter; use `doctor` and a bot chat to check your model. Anthropic uses its native Fantasy adapter.

`daily_tokens` defaults to 50,000. Before each call, reserve input byte length plus 2,048 output tokens conservatively; replace the reservation with reported usage after a successful call. Unknown/failed usage keeps its reservation. Accounting resets at UTC midnight. Provider billing limits must also be configured in the provider account. A reservation budget error can occur before all remaining tokens are spent.

Optional environment variables:

```text
BRAVE_API_KEY=...          # official Brave Search API; omit when supplying seed URLs
OPENALEX_API_KEY=...       # register with OpenAlex if required for your usage
```

Campaign discovery checks at most 10 seed URLs / 8 search results per run and adds at most five new professors in one discovery job. Discovery runs daily; manually pressing `d` starts another bounded run. Use individual faculty/lab pages with published emails. No email guessing, paywall bypass, or browser automation occurs. Crawling fails closed when robots cannot be checked, except missing robots.txt (404).

For PDFs install Poppler's `pdftotext`: `sudo apt install poppler-utils` on Ubuntu or `brew install poppler` on macOS. On Windows install Poppler and add its `Library/bin` or `bin` directory to PATH. Without it, accessible abstracts still work and are marked accordingly. Downloads are capped at 12 MiB, HTML parsing at 3 MiB, and PDF extraction at the first 25 pages / 160 KB of output. The Analyst receives a bounded excerpt; it cannot claim to have evaluated the entire paper.

## Gmail OAuth

1. Create a Google Cloud project, enable Gmail API, and configure an OAuth consent screen for your own account.
2. Create an OAuth **Desktop app** client. Set `gmail_client_id` through `configure` and `COLDFLOW_GMAIL_CLIENT_SECRET` in your environment.
3. Run `coldflow auth gmail`. Open the printed URL and consent to Gmail read and send scopes. The loopback callback listens on `127.0.0.1:53682/callback` for five minutes, validates state, and uses PKCE.
4. On a VPS, open the SSH session with `ssh -L 53682:127.0.0.1:53682 user@host` **before** starting authorization, then open the URL in your laptop browser.
5. The refresh token is saved as `gmail-token.json` with owner-only POSIX permissions. Reconnect if consent is revoked or expires. External consent apps in Google's testing mode can have short-lived refresh tokens; configure the consent app appropriately for persistent personal use.

Gmail scopes: `gmail.readonly` and `gmail.send`. Google may require verification for broader/public distribution; this version is a single-user self-hosted app. It sends through HTTPS Gmail API, so the VPS needs no outbound SMTP port.

Polling works without Pub/Sub. On first connection it establishes a current history baseline. Later restarts use the saved cursor; expired cursors trigger a recent-message resync using the last successful synchronization time. `coldflow inbox-resync` requests this recovery manually. It ignores unrelated messages and retains only tracked contact/thread replies in application storage. History/message catch-up is bounded at 1,000 messages per synchronization; excessive backlogs require narrowing/clearing the mailbox backlog before retry.

For lower-latency wakeups:

1. Enable Pub/Sub API in the same Google Cloud project.
2. Create a topic and grant `gmail-api-push@system.gserviceaccount.com` permission to publish to it.
3. Create a pull subscription. Grant your service identity subscriber access.
4. Set `gmail_topic=projects/PROJECT/topics/TOPIC` and `gmail_subscription=projects/PROJECT/subscriptions/SUB`.
5. Set `GOOGLE_APPLICATION_CREDENTIALS` to the service account credential file, or provide application default credentials through your chosen Google setup. This credential is separate from Gmail's user OAuth token.

Coldflow renews the Gmail watch before expiry, long-polls the subscription over HTTPS, and retains periodic Gmail synchronization for recovery. It does not require a public Gmail callback. [Google's setup guide](https://developers.google.com/workspace/gmail/api/guides/push).

## Discord

1. Create a Discord application and bot; invite it to a private server/channel. Grant View Channel and Send Messages, plus `applications.commands` for interactions.
2. Set `DISCORD_BOT_TOKEN`, and configure `discord_channel`, `discord_user` (your user ID), and `discord_public_key` (application public key).
3. Publish `https://YOUR_DOMAIN/webhooks/discord` as the Interactions Endpoint URL. Coldflow validates signatures and timestamps and responds to Discord's verification ping.
4. Set `DISCORD_APPLICATION_ID`, optionally `DISCORD_GUILD_ID`, and run `coldflow discord-register` once. It registers `/coldflow text:... agent:...`.
5. Enable command permission for your operator in the server integration settings; commands default to restricted access. The configured user allowlist is enforced independently.

Examples: `/coldflow text:status`, `/coldflow agent:Scout text:remember Focus on systems professors`, `/coldflow text:pause`. Replies go to the configured private channel. Sending approvals remain in the dashboard. No Message Content privileged intent or always-connected Discord Gateway is required.

## WhatsApp

Uses the official WhatsApp Business Cloud API, with its account, number, messaging-window, template, and pricing requirements. No WhatsApp Web/QR session scraping.

1. Provision a Meta developer app, WhatsApp Business account, and sender number. Obtain the **phone number ID**, not the visible phone number.
2. Set `WHATSAPP_TOKEN`, `WHATSAPP_APP_SECRET` (Meta app secret), and a random `WHATSAPP_VERIFY_TOKEN`.
3. Configure `whatsapp_phone_id`, your allowlisted `whatsapp_user` as international digits, `whatsapp_template`, and `whatsapp_language`.
4. Expose `https://YOUR_DOMAIN/webhooks/whatsapp` with valid TLS; enter your verify token and subscribe to message events in Meta.
5. Have the allowlisted operator opt in. For proactive alerts outside the service window, create an approved notification template whose body accepts **one text parameter** (`{{1}}`) for Coldflow's notification. Its name/language must match configuration.

Inside a recent operator-initiated conversation Coldflow sends free-form text; outside it, it uses the configured template. The app uses a conservative 23-hour cutoff and signed webhook message timestamps. Missing/blocked templates appear as failed notification jobs. You may enable Discord alongside WhatsApp to retain a second alert route.

Message `Scout: remember ...`, `Composer: ...`, `status`, `pause`, or `resume`. Only the configured operator number is accepted. Dashboard approval is required for approval-mode emails. `graph_version` defaults to `v23.0` and can be updated to a supported version for your Meta app; confirm support in Meta's console when provisioning.

## Notion

Create an internal Notion integration, set `NOTION_TOKEN`, and share a database with it. Configure its **data source ID** as `notion_data_source` (the data source ID may differ from the database/page ID). The adapter uses API version `2025-09-03`.

Create these exact properties:

| Property | Type |
|---|---|
| Name | Title |
| Coldflow ID | Text |
| University | Text |
| Email | Email |
| Campaign | Text |
| Paper | Text |
| Source | URL |
| Status | Select |
| Notes | Text |
| Next action | Date |
| Sent | Date |
| Replied | Date |

Add Status options: `discovered`, `drafted`, `sent`, `replied`, `declined`, `denied`, `meeting`, `meeting arranged`, `bounced`, `opt-out`, `stop`. Coldflow creates/updates one page per contact, finding existing pages by Coldflow ID after a retry. It synchronizes approximately every five minutes while running, querying pages changed since the last watermark and exporting at most 20 dirty contacts per pass. Larger initial imports take multiple passes.

Status, Notes, and Next action are imported. Other fields are generated by Coldflow. Conflicting simultaneous changes retain local fields and produce an activity event; remote stop/reply/decline/meeting statuses take priority and cancel pending outreach. Notion never grants sending approval. Avoid moving/deleting managed pages while synchronization is active. [Notion request limits](https://developers.notion.com/reference/request-limits).

## Configuration reference

`config.json` contains preferences only. Stop the service before hand-editing; restart after `configure`.

| Setting | Default / meaning |
|---|---|
| listen | `127.0.0.1:8765`, local API and proxied webhook listener |
| timezone | `Asia/Kolkata` |
| demo | false; immutable for the current service process |
| daily_tokens | 50,000 reserved/reported tokens, UTC day |
| daily_new_limit / daily_followup_limit | 10 / 10 across all campaigns |
| send_start_hour / send_end_hour | 9 / 17, local time; weekdays only |
| poll_seconds | 60; minimum 15 |
| wish_listen | blank disables embedded SSH; e.g. `0.0.0.0:23234` |
| authorized_keys | required existing public-key allowlist for Wish |
| reduced_motion | false; true disables spring feedback |

Template fields are `{{.Professor}}`, `{{.Name}}`, `{{.Background}}`, `{{.Interests}}`, `{{.Paper}}`, `{{.Connection}}`, `{{.Goal}}`, `{{.Signature}}`. Attachments are selected from the library and copied to owned storage. Individual files are limited to 10 MiB, combined email attachments to 15 MiB. Replacing a stored file invalidates delivery checks.
