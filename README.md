# Loopany Go Implementation

A Go reimplementation of Loopany Platform - multi-user scheduled agent loops.

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                            Server                                    │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐              │
│  │  Scheduler   │  │  Gateway     │  │  Store       │              │
│  │  (cron)      │──│  (HTTP API)  │──│  (Postgres)  │              │
│  └──────────────┘  └──────────────┘  └──────────────┘              │
│         │                  │                  ▲                     │
│         ▼                  ▼                  │                     │
│  ┌──────────────────────────────────────────────┐                  │
│  │         PostgreSQL (machines/loops/runs)     │                  │
│  └──────────────────────────────────────────────┘                  │
└─────────────────────────────────────────────────────────────────────┘
                              ▲
                              │ HTTP Poll
                              ▼
┌─────────────────────────────────────────────────────────────────────┐
│                            Daemon                                    │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐              │
│  │  Poll Loop   │  │  Runner      │  │  Workflow    │              │
│  │  (idle wait) │──│  (claude)    │──│  (gate)      │              │
│  └──────────────┘  └──────────────┘  └──────────────┘              │
│         │                  │                  │                     │
│         ▼                  ▼                  ▼                     │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐              │
│  │  Watcher     │  │  Artifact    │  │  Callback    │              │
│  │  (sync)      │  │  (parse)     │  │  (CLI)       │              │
│  └──────────────┘  └──────────────┘  └──────────────┘              │
└─────────────────────────────────────────────────────────────────────┘
```

## Key Design Points

### 1. BYOA (Bring Your Own Agent)
- Server runs no LLM, only schedules
- Daemon executes claude/codex/grok locally
- User's own API keys

### 2. HTTP Poll + Long-Poll
- Short poll (3s) when run in flight → progress heartbeat
- Long poll (20s hold) when idle → near-zero latency
- Stateless, auto-recover from disconnects

### 3. Workflow Gate
- Pure JS pre-processing before agent
- Can return direct message without LLM call
- Supports MCP tool calls for data fetching

### 4. Callback Mode
- Agent calls `loopany report/show/reschedule/finish`
- Run token injected via environment
- Unified CLI interface

### 5. Artifact Tracking
- Parse Claude transcript for created/edited files
- No git dependency
- Sync to server content-addressed

### 6. Transient Retry
- Auto-resume session on network errors
- Bounded retry with backoff
- Preserve paid-for progress

## Project Structure

```
loopany-go/
├── cmd/
│   ├── loopanyd/      # Daemon entry point
│   ├── loopany/       # CLI callback entry point
│   └── server/        # Server entry point
├── internal/
│   ├── daemon/        # Main poll loop
│   ├── server/        # HTTP Gateway + Scheduler
│   ├── runner/        # Agent execution
│   ├── workflow/      # JS gate
│   ├── artifact/      # Transcript parsing
│   ├── watcher/       # File sync
│   └── store/         # Database operations
├── pkg/
│   ├── poll/          # HTTP poll client
│   ├── config/        # Config persistence
│   └── token/         # Token management
└── internal/protocol/ # API types
```

## Quick Start

### Prerequisites
- Go 1.22+
- PostgreSQL (or use embedded)

### Build
```bash
cd /data/code/loopany-go

# Download dependencies
go mod tidy

# Build binaries
go build -o bin/loopany-server ./cmd/server
go build -o bin/loopanyd ./cmd/loopanyd
go build -o bin/loopany ./cmd/loopany
```

### Run Server
```bash
# Set environment
export DATABASE_URL="postgres://user:pass@localhost/loopany?sslmode=disable"
export TOKEN_SECRET="your-secret-key-change-in-production"

# Run migrations and start
./bin/loopany-server --addr :3000
```

### Run Daemon
```bash
# Connect to server with device token
./bin/loopanyd --server-url http://localhost:3000 --api-key YOUR_DEVICE_TOKEN
```

### Create Loop (via API)
```bash
curl -X POST http://localhost:3000/api/loops \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Daily Report",
    "cron": "0 9 * * *",
    "workdir": "/home/user/projects/report",
    "enabled": true
  }'
```

## Environment Variables

### Server
- `DATABASE_URL` - PostgreSQL connection string
- `TOKEN_SECRET` - Token signing secret
- `ADDR` - Server address (default :3000)

### Daemon
- `LOOPANY_SERVER_URL` - Server URL
- `LOOPANY_TOKEN` - Device token
- `LOOPANY_ROOTS` - Comma-separated workdir jail
- `LOOPANY_EXEC_TIMEOUT_MS` - Run timeout (0 = unlimited)
- `LOOPANY_TRANSIENT_RETRIES` - Retry count (default 2)
- `LOOPANY_CLAUDE_BIN` - Claude binary override
- `LOOPANY_CODEX_BIN` - Codex binary override
- `LOOPANY_GROK_BIN` - Grok binary override

### Workflow
- `LOOPANY_WORKFLOW_TIMEOUT_SECONDS` - Workflow timeout (default 30)

## API Endpoints

### Machine Gateway (Daemon)
- `POST /api/machine/poll` - Claim pending runs
- `POST /machine/report` - Finalize run
- `POST /api/machine/cli` - CLI callback
- `POST /api/machine/sync` - Artifact sync

### Owner API
- `GET /api/loops` - List loops
- `POST /api/loops` - Create loop
- `GET /api/loops/{id}` - Get loop
- `PATCH /api/loops/{id}` - Update loop
- `DELETE /api/loops/{id}` - Disable loop
- `GET /api/loops/{id}/runs` - List runs

### Health
- `GET /health` - Health check

## CLI Commands (inside agent)

```bash
# Report progress
loopany report --message "Processed 10 items"

# Show loop state
loopany show

# Reschedule next run
loopany reschedule --delay "1h"

# Finish closed loop
loopany finish --reason "Goal achieved"

# Update schedule
loopany set-cron --cron "0 */2 * * *"
```

## Comparison with Original

| Feature | TypeScript (Original) | Go (This) |
|---------|----------------------|-----------|
| HTTP Server | TanStack Start | net/http |
| Cron Engine | croner | robfig/cron |
| Logging | pino | log |
| DB ORM | Drizzle | sql + manual SQL |
| Blob Storage | R2 | R2/local |
| Auth | Better Auth | HMAC tokens |
| Lines of Code | ~8,000 | ~5,000 |

## Execution Logging

Loopany now provides detailed execution logging to help you understand what's happening during agent runs:

### Real-time Output

When running `loopanyd`, you'll see detailed execution progress:

```
════════════════════════════════════════════════════════════
🚀 Starting Run: 1234567890
   Loop: Daily Report (loop-abc-123)
   Workdir: /home/user/projects/report
   Agent: claude-code
   Model: claude-sonnet-5
════════════════════════════════════════════════════════════
🤖 Starting agent execution...
   Agent type: claude-code
   Workdir: /home/user/projects/report
📝 Prompt length: 1234 characters
────────────────────────────────────────────────────────────────
[RUN-1234567890][100.234ms][cmd] claude -p '...' --output-format stream-json --verbose
[RUN-1234567890][150.567ms][tool_call] Read: {"file_path":"/home/user/projects/report/task.md"}
[RUN-1234567890][200.123ms][output] {"type":"tool_result","content":"..."}
[RUN-1234567890][250.456ms][tool_call] Edit: {"file_path":"/home/user/projects/report/output.md"}
[RUN-1234567890][300.789ms][status] Editing file...
────────────────────────────────────────────────────────────────
📦 Parsing artifacts from session...
   Session ID: sess-xyz-789
   Exit code: 0
   Duration: 5.234s
   Cost: $0.0234 (in: 1234, out: 567, cache: 890/123)
   Artifacts: 2 files
      - output.md (created)
      - report.md (edited)
────────────────────────────────────────────────────────────────
📡 Reporting to server...
✅ Run completed successfully in 5234ms
════════════════════════════════════════════════════════════
```

### Log Viewer Tool

Use `loopany-log` to view execution history:

```bash
# List all logs
./bin/loopany-log list

# Show specific log
./bin/loopany-log show 1234567890

# Tail most recent log
./bin/loopany-log tail

# Watch for new logs in real-time
./bin/loopany-log watch
```

### Log Files

All execution logs are saved to `/tmp/loopany-exec/<run-id>.log` with structured JSON:

```json
{"time":"2026-07-18T10:30:45.123Z","type":"start","message":"Agent execution starting in /home/user/projects/report"}
{"time":"2026-07-18T10:30:45.234Z","type":"cmd","message":"claude -p '...' --output-format stream-json --verbose"}
{"time":"2026-07-18T10:30:45.567Z","type":"tool_call","message":"Read: {\"file_path\":\"...\"}"}
{"time":"2026-07-18T10:30:45.890Z","type":"output","message":"{\"type\":\"tool_result\",...}"}
{"time":"2026-07-18T10:30:50.123Z","type":"end","message":"Execution completed: exit=0, session=sess-xyz-789, duration=5.234s"}
```

### What's Logged

- **Commands**: Exact command line with arguments
- **Tool Calls**: Every tool invocation (Read, Edit, Bash, etc.)
- **Output**: Real-time output lines
- **Status**: Progress indicators (Thinking, Reading, Editing)
- **Errors**: Detailed error messages with context
- **Metrics**: Duration, cost, token usage, artifacts

## Next Steps

1. **Blob Storage** - Add R2/S3 content-addressed storage
2. **Metrics** - Prometheus metrics endpoint
3. **Web UI** - React dashboard for loop management
4. **Tests** - Unit + integration tests
5. **Docker** - Container images for deployment
6. **Docs** - OpenAPI spec and user guide

## License

MIT