#!/bin/bash
# Loop 删除功能验证脚本

set -e

BASE_URL="${1:-http://localhost:3000/loopany}"
TEST_LOOP_NAME="Test Loop $(date +%s)"

echo "🔧 Loop 删除功能验证"
echo "API 地址: $BASE_URL"
echo ""

# 1. 创建测试 loop
echo "1️⃣ 创建测试 loop..."
LOOP_DATA=$(cat <<EOF
{
  "name": "$TEST_LOOP_NAME",
  "task": "This is a test task for deletion verification",
  "goal": "Test deletion functionality",
  "workdir": "/tmp/loopany-test",
  "model": "glm-5",
  "agent": "claude-code",
  "enabled": true
}
EOF
)

RESPONSE=$(curl -s -X POST "$BASE_URL/api/loops" \
  -H "Content-Type: application/json" \
  -d "$LOOP_DATA")

LOOP_ID=$(echo "$RESPONSE" | grep -o '"id":"[^"]*"' | cut -d'"' -f4)

if [ -z "$LOOP_ID" ]; then
  echo "❌ 创建 loop 失败"
  echo "$RESPONSE"
  exit 1
fi

echo "✅ Loop 创建成功"
echo "   ID: $LOOP_ID"
echo "   Name: $TEST_LOOP_NAME"
echo ""

# 2. 验证 loop 存在
echo "2️⃣ 验证 loop 存在..."
LOOP_CHECK=$(curl -s "$BASE_URL/api/loops/$LOOP_ID")

if echo "$LOOP_CHECK" | grep -q "$LOOP_ID"; then
  echo "✅ Loop 存在验证通过"
else
  echo "❌ Loop 不存在"
  exit 1
fi
echo ""

# 3. 删除 loop
echo "3️⃣ 删除 loop..."
DELETE_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE "$BASE_URL/api/loops/$LOOP_ID")

if [ "$DELETE_STATUS" = "204" ]; then
  echo "✅ Loop 删除成功 (HTTP $DELETE_STATUS)"
else
  echo "❌ 删除失败 (HTTP $DELETE_STATUS)"
  exit 1
fi
echo ""

# 4. 验证 loop 已被删除
echo "4️⃣ 验证 loop 已被删除..."
DELETE_CHECK=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/api/loops/$LOOP_ID")

if [ "$DELETE_CHECK" = "404" ]; then
  echo "✅ Loop 已被永久删除 (HTTP $DELETE_CHECK)"
else
  echo "❌ Loop 仍然存在 (HTTP $DELETE_CHECK)"
  exit 1
fi
echo ""

# 5. 验证 loop 不在列表中
echo "5️⃣ 验证 loop 不在列表中..."
LOOPS_LIST=$(curl -s "$BASE_URL/api/loops")

if echo "$LOOPS_LIST" | grep -q "$LOOP_ID"; then
  echo "❌ Loop 仍然在列表中"
  exit 1
else
  echo "✅ Loop 已从列表中移除"
fi
echo ""

echo "━━━━━━━━━━━━━━━━━━━━━━"
echo "✅ 所有验证通过！"
echo ""
echo "删除功能正常工作："
echo "  - DELETE 请求返回 204 No Content"
echo "  - 删除后 GET 请求返回 404 Not Found"
echo "  - Loop 不再出现在列表中"
echo ""
echo "结论: Loop 删除功能已正确实现硬删除"
echo "━━━━━━━━━━━━━━━━━━━━━━"