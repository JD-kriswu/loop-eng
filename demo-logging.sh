#!/bin/bash

# Demo script to show improved execution logging

echo "════════════════════════════════════════════════════════════"
echo "Loopany Execution Logging Demo"
echo "════════════════════════════════════════════════════════════"
echo ""

echo "1. Check existing logs:"
echo "   ./bin/loopany-log list"
echo ""

if [ -d "/tmp/loopany-exec" ]; then
    ./bin/loopany-log list
else
    echo "   No logs directory yet (will be created on first execution)"
fi

echo ""
echo "2. Monitor execution in real-time:"
echo "   ./bin/loopanyd --server-url <url> --api-key <token>"
echo ""
echo "   You'll see:"
echo "   🚀 Starting Run: <run-id>"
echo "      Loop: <name> (<id>)"
echo "      Workdir: <path>"
echo "      Agent: <type>"
echo "   ════════════════════════════════════════════════════════════"
echo "   🤖 Starting agent execution..."
echo "   [RUN-<id>][123.456ms][cmd] claude -p '...' --output-format stream-json --verbose"
echo "   [RUN-<id>][234.567ms][tool_call] Read: {\"file_path\":\"...\"}"
echo "   [RUN-<id>][345.678ms][output] {\"type\":\"tool_result\",...}"
echo "   [RUN-<id>][456.789ms][status] Reading file..."
echo "   ────────────────────────────────────────────────────────────────"
echo "   ✅ Run completed successfully in 567ms"
echo ""

echo "3. View detailed execution logs:"
echo "   ./bin/loopany-log show <run-id>"
echo ""
echo "   Output includes:"
echo "   - Exact commands executed"
echo "   - Tool calls (Read, Edit, Bash, etc.)"
echo "   - Real-time output"
echo "   - Error messages with context"
echo "   - Duration and cost metrics"
echo ""

echo "4. Tail most recent log:"
echo "   ./bin/loopany-log tail"
echo ""

echo "5. Watch for new logs:"
echo "   ./bin/loopany-log watch"
echo ""

echo "════════════════════════════════════════════════════════════"
echo "Log files location: /tmp/loopany-exec/<run-id>.log"
echo "════════════════════════════════════════════════════════════"