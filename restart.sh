#!/bin/bash
# Loopany 服务编译和重启脚本
# 使用方法: ./restart.sh [port]

set -e

# 配置
PORT="${1:-3001}"
SERVICE_NAME="loopany-server"
DB_URL="postgres://loopany:loopany@localhost:5432/loopany?sslmode=disable"
TOKEN_SECRET="dev-secret-change-in-production"
LOG_DIR="logs"
LOG_FILE="$LOG_DIR/server.log"

echo "━━━━━━━━━━━━━━━━━━━━━━"
echo "🔧 Loopany 服务重启脚本"
echo "━━━━━━━━━━━━━━━━━━━━━━"
echo "端口: $PORT"
echo ""

# 1. 确保日志目录存在
echo "1️⃣ 准备环境..."
mkdir -p $LOG_DIR

# 2. 查找并停止旧进程
echo "2️⃣ 检查运行中的服务..."
OLD_PID=$(lsof -ti:$PORT 2>/dev/null || true)

if [ -n "$OLD_PID" ]; then
    echo "   发现进程 PID: $OLD_PID"
    kill $OLD_PID 2>/dev/null || true
    sleep 2

    # 强制杀死进程（如果还在运行）
    if lsof -ti:$PORT >/dev/null 2>&1; then
        echo "   强制停止进程..."
        kill -9 $(lsof -ti:$PORT) 2>/dev/null || true
        sleep 1
    fi
    echo "   ✅ 旧进程已停止"
else
    echo "   ✅ 没有运行中的服务"
fi

# 3. 编译新版本
echo "3️⃣ 编译新版本..."
go build -o bin/$SERVICE_NAME ./cmd/server
if [ $? -eq 0 ]; then
    echo "   ✅ 编译成功"
else
    echo "   ❌ 编译失败"
    exit 1
fi

# 4. 启动新服务
echo "4️⃣ 启动新服务..."
export DATABASE_URL="$DB_URL"
export TOKEN_SECRET="$TOKEN_SECRET"

# 清空日志文件
> $LOG_FILE

# 启动服务
nohup ./bin/$SERVICE_NAME --addr :$PORT > $LOG_FILE 2>&1 &
NEW_PID=$!
sleep 2

# 5. 验证服务启动
echo "5️⃣ 验证服务状态..."

# 检查进程是否运行
if ps -p $NEW_PID > /dev/null 2>&1; then
    echo "   ✅ 服务进程运行中 (PID: $NEW_PID)"
else
    echo "   ❌ 服务启动失败"
    echo ""
    echo "最后 20 行日志："
    tail -n 20 $LOG_FILE
    exit 1
fi

# 检查端口是否监听
if netstat -tlnp 2>/dev/null | grep -q ":$PORT"; then
    echo "   ✅ 端口 $PORT 正在监听"
else
    echo "   ❌ 端口 $PORT 未监听"
    echo ""
    echo "最后 20 行日志："
    tail -n 20 $LOG_FILE
    exit 1
fi

# 测试 API
sleep 1
HEALTH=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:$PORT/api/stats 2>/dev/null || echo "000")

if [ "$HEALTH" = "200" ]; then
    echo "   ✅ API 响应正常 (HTTP $HEALTH)"
else
    echo "   ⚠️  API 响应异常 (HTTP $HEALTH)"
    echo "   这可能是正常的，如果数据库还在初始化"
fi

# 6. 显示状态
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━"
echo "✅ 服务重启成功！"
echo "━━━━━━━━━━━━━━━━━━━━━━"
echo "📊 服务信息:"
echo "   端口: $PORT"
echo "   PID: $NEW_PID"
echo "   日志: $LOG_FILE"
echo "   API: http://localhost:$PORT"
echo ""
echo "📝 快速命令:"
echo "   查看日志: tail -f $LOG_FILE"
echo "   停止服务: kill $NEW_PID"
echo "   检查状态: curl -s http://localhost:$PORT/api/stats | jq ."
echo "━━━━━━━━━━━━━━━━━━━━━━"