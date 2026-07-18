#!/bin/bash
# Loopany 服务状态检查脚本
# 使用方法: ./status.sh [port]

PORT="${1:-3001}"

echo "📊 Loopany 服务状态检查"
echo "━━━━━━━━━━━━━━━━━━━━━━"
echo "端口: $PORT"
echo ""

# 检查进程
PID=$(lsof -ti:$PORT 2>/dev/null || true)

if [ -z "$PID" ]; then
    echo "状态: ❌ 未运行"
    echo ""
    echo "启动服务: ./restart.sh $PORT"
    exit 1
fi

# 获取进程信息
PROCESS_INFO=$(ps -p $PID -o pid,vsz,rss,pcpu,time,comm 2>/dev/null | tail -n 1)

echo "状态: ✅ 运行中"
echo ""
echo "进程信息:"
echo "  PID: $PID"
echo "  详情: $PROCESS_INFO"
echo ""

# 检查端口监听
PORT_INFO=$(netstat -tlnp 2>/dev/null | grep ":$PORT" || true)
if [ -n "$PORT_INFO" ]; then
    echo "端口监听: ✅"
else
    echo "端口监听: ❌"
fi

# 测试 API
echo ""
echo "API 测试:"
STATS=$(curl -s -m 3 http://localhost:$PORT/api/stats 2>/dev/null || true)

if [ -n "$STATS" ]; then
    echo "  状态: ✅ 正常"
    echo ""
    echo "统计数据:"

    # 解析 JSON（使用 grep 和 sed，不依赖 jq）
    TOTAL_RUNS=$(echo "$STATS" | grep -o '"total_runs":[0-9]*' | cut -d':' -f2)
    ACTIVE_LOOPS=$(echo "$STATS" | grep -o '"active_loops":[0-9]*' | cut -d':' -f2)
    TOTAL_COST=$(echo "$STATS" | grep -o '"total_cost":[0-9.]*' | cut -d':' -f2)

    echo "  总运行次数: $TOTAL_RUNS"
    echo "  活跃 Loops: $ACTIVE_LOOPS"
    echo "  总成本: \$$TOTAL_COST"
else
    echo "  状态: ❌ 无响应"
fi

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━"