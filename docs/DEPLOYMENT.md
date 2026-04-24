# Running Coldflow

## Oracle / other Linux VPS

Use an Ubuntu/Oracle Linux VM with 1 vCPU and 1 GB RAM, persistent local disk, working DNS, and outbound HTTPS. The instructions below use Ubuntu and systemd. Oracle Cloud's VCN security rules and the guest firewall both need to permit your chosen inbound ports. Leave the local API on `127.0.0.1`.

Build on your laptop/CI with `scripts/build.ps1` or `scripts/build.sh` and copy the matching Linux binary to the VM. Choose `linux-amd64` for an x86 VM or `linux-arm64` for ARM; `uname -m` identifies the architecture. Building Fantasy/SQLite on the 1-GB VM is unnecessarily expensive. No container is required.

```sh
sudo useradd --system --create-home --home-dir /var/lib/coldflow --shell /usr/sbin/nologin coldflow
sudo install -m 0755 coldflow-linux-amd64 /usr/local/bin/coldflow
sudo install -d -o coldflow -g coldflow -m 0700 /var/lib/coldflow
sudo install -d -o root -g coldflow -m 0750 /etc/coldflow
sudo apt update
sudo apt install poppler-utils ca-certificates
sudo -u coldflow /usr/local/bin/coldflow --data /var/lib/coldflow init
sudo -u coldflow /usr/local/bin/coldflow --data /var/lib/coldflow configure
```

Copy `deploy/coldflow.env.example` to `/etc/coldflow/coldflow.env`, fill in the required secrets, and set owner `coldflow:coldflow`, mode `0600`. Place optional Google application credentials there with the same ownership and permissions. Keep secrets outside your repository.

For the one-time Gmail authorization, forward the callback in your laptop's SSH session:

```sh
ssh -L 53682:127.0.0.1:53682 YOUR_USER@VPS
```

In that session, start authorization with the service environment loaded. Use only simple `KEY=value` entries compatible with both your shell and systemd's EnvironmentFile parser:

```sh
sudo -u coldflow sh -c 'set -a; . /etc/coldflow/coldflow.env; set +a; exec /usr/local/bin/coldflow --data /var/lib/coldflow auth gmail'
```

Open the printed URL on your laptop. Then install the unit:

```sh
sudo install -m 0644 deploy/coldflow.service /etc/systemd/system/coldflow.service
sudo systemctl daemon-reload
sudo systemctl enable --now coldflow
sudo systemctl status coldflow
sudo journalctl -u coldflow -f
```

Open the dashboard through normal SSH:

```sh
sudo -u coldflow /usr/local/bin/coldflow --data /var/lib/coldflow tui
```

`q` closes this client; systemd keeps the agents and mailbox watcher running. The unit reserves filesystem access to `/var/lib/coldflow`, sets a 350-MiB Go memory target, a 450-MB memory-high threshold, and a 650-MB cgroup maximum. These are limits, not measured guarantees. Leave capacity for the OS and optional TLS proxy. Investigate repeated OOM restarts rather than increasing concurrency.

Default worker count is one, research HTTP access is bounded/sequential, no AI inference runs locally, and logs/history/cache sizes are bounded. PDF extraction runs one subprocess at a time with time, page, download, and output limits. Repeated failures appear in Jobs; retry after fixing the cause.

### HTTPS callbacks

Discord interactions and WhatsApp incoming chat require a reachable HTTPS endpoint. Point an A/AAAA record at the VPS, install Caddy through its official distribution instructions, and adapt `deploy/Caddyfile`. Permit inbound TCP 80/443 in both Oracle security rules and the guest firewall; keep SSH restricted as appropriate for your access.

Caddy handles TLS and forwards **only** `/webhooks/discord` and `/webhooks/whatsapp` to Coldflow. It returns 404 for the local API. Webhooks are signed and operator-allowlisted. Configure the two endpoint URLs in the provider consoles after Caddy is serving valid TLS. Gmail's optional Pub/Sub uses outbound pull and needs no public route.

### Optional Wish SSH dashboard

Set `wish_listen` to `0.0.0.0:23234` and `authorized_keys` to `/var/lib/coldflow/authorized_keys`. Put your **public** SSH keys there, owned by the service user; do not copy private keys. Restart the service and permit that port in the firewall/VCN.

```sh
ssh -p 23234 -t coldflow@VPS
```

The embedded server uses public-key allowlisting, disables password auth, creates its own persistent host key, and permits two dashboard sessions. Verify its host key on first connection. Normal OpenSSH remains the primary administration route.

## Windows laptop

```powershell
go build -trimpath -o "$HOME\coldflow.exe" ./cmd/coldflow
& "$HOME\coldflow.exe" init
& "$HOME\coldflow.exe" configure
$env:COLDFLOW_AI_KEY = 'your-key'
$env:COLDFLOW_GMAIL_CLIENT_SECRET = 'your-oauth-client-secret'
& "$HOME\coldflow.exe" auth gmail
& "$HOME\coldflow.exe" serve
```

Open another terminal and run `& "$HOME\coldflow.exe" tui`. Use Windows Terminal with a modern PowerShell. The default `%APPDATA%\coldflow` directory must be accessible only to your Windows user; POSIX mode bits do not replace Windows ACLs.

For automatic startup use Task Scheduler: create a task for your user at logon, choose the executable, and use `--data "C:\Users\YOU\AppData\Roaming\coldflow" serve` as arguments. Set the working directory to a local folder. Ensure credentials are available to the task's environment (restart your sign-in/task after changing user environment variables). Disable overlapping task instances and enable restart on failure. A logon task runs while your user session is active; use a dedicated service wrapper if you need operation before logon.

Sleep, shutdown, and disconnection stop mailbox processing. On waking/restarting, saved Gmail history is reconciled and overdue sends retain spacing and daily limits. No laptop can provide 24/7 monitoring while powered off.

## macOS laptop

Build/install the `darwin-arm64` binary for Apple Silicon or `darwin-amd64` for Intel. If macOS blocks a binary copied from your own build machine, approve it through your normal system security workflow.

```sh
go build -trimpath -o coldflow ./cmd/coldflow
sudo install -m 0755 coldflow /usr/local/bin/coldflow
coldflow init
coldflow configure
export COLDFLOW_AI_KEY='your-key'
export COLDFLOW_GMAIL_CLIENT_SECRET='your-oauth-client-secret'
coldflow auth gmail
coldflow serve
```

For login startup, copy `deploy/com.coldflow.agent.plist` to `~/Library/LaunchAgents/`, replace every `YOUR_USER`, and add your provider environment variables to the local plist's EnvironmentVariables dictionary. Set mode `0600` because this local copy contains secrets. Load it with `launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.coldflow.agent.plist`. `launchd` does not inherit shell exports. Rotate the configured log files periodically. Use `launchctl bootout` before changing the job or upgrading its binary.

The laptop must remain awake and online for monitoring. A VPS remains the recommended persistent host.

## Backups and upgrades

```sh
coldflow --data /var/lib/coldflow backup /PRIVATE_LOCATION/coldflow-backup.zip
coldflow --data /NEW_EMPTY_DIRECTORY restore /PRIVATE_LOCATION/coldflow-backup.zip
```

Backups use a consistent SQLite snapshot and include attachments, preferences, API token, Gmail token, and optional Wish host keys. They contain credentials; keep them private and encrypt them using your backup system. Provider environment files and Google application credentials outside the data directory must be backed up separately. Attachment paths are rewritten on restore. The destination must be empty. Verify external authorized-key/credential paths with `doctor` before starting a restored service.

For an upgrade: pause sending, take a backup, stop the service, replace the binary, start it, inspect health/Jobs, then resume. Schema version 1 is recorded in the migrations table. Future incompatible changes must ship explicit migrations and restore instructions; do not downgrade a database blindly.

Do not run two services against one installation or copy a live SQLite DB without its WAL. A service bind check prevents duplicate instances using the same configured listener. Separate instances must use separate data directories and mail accounts.

## Monitoring and recovery

- `coldflow doctor`: required local configuration plus optional integration/tool checks; does not send test messages.
- `coldflow status`: authenticated service counts. `/healthz` reports that the HTTP process is alive, not full integration readiness.
- Settings: inbox sync time, provider errors, Notion state, and AI tokens. Jobs: retry failed work after fixing account/network/source issues.
- `coldflow inbox-resync`: request a bounded recovery scan after mailbox history expiry.
- Uncertain delivery: Coldflow searches Gmail by its stable Message-ID. If absent, it stops without resending. Inspect Gmail Sent, then use Drafts `u` to mark it not delivered and review/approve the new revision. A found message is reconciled as sent.
- Reply/stop/bounce suppression prevents further automatic outreach to that address. Changing a tracking label does not remove a suppression entry.
- OAuth token errors: reconnect Gmail; dashboard and queued research remain available.

Run `sh scripts/soak.sh /PATH/coldflow /NEW_DEMO_DIRECTORY 86400` on the target VM for a 24-hour **demo/idle** soak. The script records RSS/CPU and checks service access every minute. Exercise real research, attachments, reconnects, and test-recipient delivery separately before enabling automatic campaigns. Never use the demo soak result as proof of live-provider memory usage.
