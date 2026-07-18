# Loopany 运维脚本

这个目录包含了一系列运维脚本，用于简化服务的编译、启动、停止和状态检查。

## 快速开始

```bash
# 重启服务（默认 3001 端口）
./restart.sh

# 重启服务（指定端口）
./restart.sh 3002

# 查看状态
./status.sh

# 停止服务
./stop.sh

# 验证删除功能
./verify_delete.sh http://localhost:3001
```

## 脚本说明

### restart.sh - 编译并重启服务

**功能：**
- 自动停止旧进程
- 编译最新代码
- 启动新服务
- 验证服务状态

**使用方法：**
```bash
# 默认端口 3001
./restart.sh

# 指定端口
./restart.sh 3002
```

**特点：**
- ✅ 自动检查并停止旧进程
- ✅ 强制杀死不响应的进程
- ✅ 自动创建日志目录
- ✅ 正确设置数据库连接
- ✅ 验证服务启动成功
- ✅ 显示服务信息和快速命令

### stop.sh - 停止服务

**功能：**
- 优雅停止服务
- 如果不响应则强制停止

**使用方法：**
```bash
# 默认端口 3001
./stop.sh

# 指定端口
./stop.sh 3002
```

### status.sh - 检查服务状态

**功能：**
- 检查进程是否运行
- 检查端口是否监听
- 测试 API 是否响应
- 显示统计数据

**使用方法：**
```bash
# 默认端口 3001
./status.sh

# 指定端口
./status.sh 3002
```

### verify_delete.sh - 验证删除功能

**功能：**
- 创建测试 loop
- 验证 loop 存在
- 删除 loop
- 验证硬删除成功

**使用方法：**
```bash
# 默认 http://localhost:3000/loopany
./verify_delete.sh

# 指定 API 地址
./verify_delete.sh http://localhost:3001
```

## 配置

脚本中已包含以下配置：

- **数据库**: `postgres://loopany:loopany@localhost:5432/loopany`
- **Token Secret**: `dev-secret-change-in-production`
- **默认端口**: `3001`
- **日志目录**: `logs/`

如需修改，请编辑 `restart.sh` 脚本顶部的配置变量。

## 常见问题

### 1. 端口已被占用

```bash
# 查看占用端口的进程
lsof -i:3001

# 手动停止
./stop.sh 3001
```

### 2. 数据库连接失败

确保 PostgreSQL 容器正在运行：

```bash
# 检查容器
docker ps | grep postgres

# 启动容器（如果未运行）
docker start loopany-postgres
```

### 3. 查看日志

```bash
# 实时查看日志
tail -f logs/server.log

# 查看最近 50 行
tail -n 50 logs/server.log
```

## 服务管理命令速查

```bash
# 重启服务
./restart.sh

# 查看状态
./status.sh

# 停止服务
./stop.sh

# 查看日志
tail -f logs/server.log

# 测试 API
curl -s http://localhost:3001/api/stats | jq .

# 查看进程
ps aux | grep loopany-server
```

## 开发工作流

推荐的开发工作流：

```bash
# 1. 修改代码后重启
./restart.sh

# 2. 查看状态确认
./status.sh

# 3. 查看日志（如果有问题）
tail -f logs/server.log

# 4. 验证功能（如果需要）
./verify_delete.sh http://localhost:3001
```