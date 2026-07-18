# 执行日志改进总结

## 问题描述

用户反馈：
- 当前的每一次执行，都没有输出对应的执行结果
- 不知道过程中执行了哪些命令
- 不知道卡在什么地方

## 解决方案

### 1. 实时流式输出 (runner.go)

**改进前**:
- 使用 `cmd.CombinedOutput()` 等待命令完成后才返回
- 看不到实时执行过程

**改进后**:
- 使用 `cmd.StdoutPipe()` 和 `cmd.StderrPipe()` 实时读取输出
- 立即显示每一行输出
- 解析 JSON 输出识别工具调用

```go
// 创建管道实时读取输出
stdout, err := cmd.StdoutPipe()
stderr, err := cmd.StderrPipe()

// Reader goroutine for stdout
go func() {
    scanner := bufio.NewScanner(stdout)
    for scanner.Scan() {
        line := scanner.Text()
        outputChan <- line
        outputBuilder.WriteString(line + "\n")
    }
}()
```

### 2. 结构化日志系统 (ExecutionLogger)

新增 `ExecutionLogger` 类型，记录：

- **执行步骤**: 每个步骤的时间戳、类型、消息
- **命令详情**: 执行的完整命令和参数
- **工具调用**: Read, Edit, Bash 等工具调用
- **状态更新**: Thinking, Reading, Editing 等状态
- **错误信息**: 详细的错误上下文

```go
type ExecutionStep struct {
    Time    time.Time `json:"time"`
    Type    string    `json:"type"` // "cmd", "output", "tool_call", "error"
    Message string    `json:"message"`
}
```

日志同时输出到：
1. 控制台（实时显示）
2. 文件 `/tmp/loopany-exec/<run-id>.log`（持久化）

### 3. 增强的 Daemon 日志 (daemon.go)

**改进后的输出示例**:

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

### 4. 日志查看工具 (loopany-log)

新增独立工具查看执行历史：

```bash
# 列出所有日志
./bin/loopany-log list

# 查看特定日志
./bin/loopany-log show <run-id>

# 查看最近日志
./bin/loopany-log tail

# 实时监控新日志
./bin/loopany-log watch
```

输出示例：
```
🚀 [10:30:45.123] Agent execution starting in /home/user/projects/report
📝 [10:30:45.234] 📋 claude -p '...' --output-format stream-json --verbose
🔧 [10:30:45.567] 🔧 Read: {"file_path":"..."}
✅ [10:30:45.890] ✅ tool_result received
📍 [10:30:46.123] 📍 Editing file...
🏁 [10:30:50.456] Execution completed: exit=0, session=sess-xyz-789, duration=5.234s
```

## 改进点总结

| 方面 | 改进前 | 改进后 |
|------|--------|--------|
| **输出时机** | 命令完成后 | 实时流式输出 |
| **日志内容** | 简单的开始/结束 | 完整的执行过程 |
| **命令可视化** | 无 | 显示完整命令和参数 |
| **工具追踪** | 无 | 记录所有工具调用 |
| **状态指示** | 无 | Thinking/Reading/Editing |
| **错误定位** | 难以定位 | 详细错误上下文 |
| **历史查询** | 无 | 独立日志查看工具 |
| **持久化** | 无 | 结构化 JSON 日志文件 |

## 使用方法

### 运行 Daemon

```bash
./bin/loopanyd --server-url http://localhost:3000 --api-key YOUR_TOKEN
```

你会立即看到详细的执行过程。

### 查看历史日志

```bash
# 列出所有执行记录
./bin/loopany-log list

# 查看特定执行
./bin/loopany-log show 1234567890

# 持续监控
./bin/loopany-log watch
```

### 日志文件位置

所有日志保存在：`/tmp/loopany-exec/<run-id>.log`

每行都是结构化的 JSON，便于解析和分析：

```json
{"time":"2026-07-18T10:30:45.123Z","type":"start","message":"Agent execution starting"}
{"time":"2026-07-18T10:30:45.234Z","type":"cmd","message":"claude -p '...'"}
{"time":"2026-07-18T10:30:45.567Z","type":"tool_call","message":"Read: {...}"}
{"time":"2026-07-18T10:30:50.456Z","type":"end","message":"Execution completed"}
```

## 文件修改清单

1. **internal/runner/runner.go** - 实时流式输出 + ExecutionLogger
2. **internal/daemon/daemon.go** - 增强的执行日志
3. **cmd/loopany-log/main.go** - 日志查看工具（新增）
4. **README.md** - 更新文档
5. **demo-logging.sh** - 演示脚本（新增）

## 下一步

- [ ] 添加 Web UI 实时显示日志
- [ ] 支持日志导出（CSV, JSON）
- [ ] 添加日志搜索功能
- [ ] 支持远程日志聚合
- [ ] 添加执行分析和统计