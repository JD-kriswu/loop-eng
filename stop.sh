#!/bin/bash
# Loopany 服务停止脚本
# 使用方法: ./stop.sh [port]

set -e

PORT="${1:-3001}"

echo "🛑 停止 Loopany 服务 (端口: $PORT)..."

# 查找进程
PID=$(lsof -ti:$PORT 2>/dev/null || true)

if [ -z "$PID" ]; then
    echo "✅ 端口 $PORT 上没有运行的服务"
    exit 0
fi

echo "发现进程 PID: $PID"

# 尝试优雅停止
kill $PID 2>/dev/null || true
sleep 2

# 检查是否还在运行
if lsof -ti:$PORT >/dev/null 2>&1; then
    echo "进程未响应，强制停止..."
    kill -9 $(lsof -ti:$PORT) 2>/dev/null || true
    sleep 1
fi

# 最终检查
if lsof -ti:$PORT >/dev/null 2>&1; then
    echo "❌ 无法停止服务"
    exit 1
else
    echo "✅ 服务已停止"
fi