# SimpleRemoteUpdate

一个面向 Windows 的轻量远程更新工具（单文件 `exe`），支持多程序管理、在线上传部署、回滚、版本管理、变更明细与实时日志。

## 功能概览

- 多程序独立配置（互不干扰）：`service_name`、`target_dir`、`current_version`、忽略规则等按程序保存。
- 更新流程：上传 ZIP -> 备份 -> 停服务（可选）-> 替换文件 -> 启服务（可选）-> 记录部署结果。
- 更新模式可选：
  - `full`（全部替换）：删除目标目录中“上传包不存在”的文件，适合完整发版。
  - `partial`（局部替换）：仅覆盖上传包内文件，不删除目标目录其他文件，适合增量发版。
- 回滚流程：基于历史备份包恢复，支持替换忽略规则。
- 实时日志：SSE 推送部署日志。
- 部署记录：分页懒加载（避免一次性渲染大量记录导致卡顿）。
- 配置热更新：保存后自动刷新运行配置（`listen_addr` 变更需重启进程）。

## 技术栈

- Go `1.23+`（`go.mod` 含 `toolchain go1.24.6`）
- 标准库：`net/http`、`html/template`、`archive/zip`、`//go:embed`
- Windows 服务控制：`golang.org/x/sys/windows/svc/mgr`
- 前端：HTMX + Tailwind CDN（无前端构建步骤）

## 目录结构

```text
.
├─ main.go                      # 路由、配置API、部署API
├─ deployment_runtime.go        # 部署/回滚执行
├─ file_ops.go                  # 解压、替换、忽略规则匹配
├─ store_sessions_events.go     # 部署记录、会话、SSE
├─ config_templates.go          # 默认配置与模板函数
├─ web/
│  ├─ templates/                # 页面与局部模板
│  └─ static/                   # 前端脚本与样式
├─ config.json                  # 运行配置
└─ data/                        # 上传包、备份、日志、部署记录
```

## 快速开始

```bash
go run .
# 或
go build -o updater.exe .
```

访问：`http://127.0.0.1:8090`（默认）

首次登录注意：`auth_key_sha256` 存储的是密钥的 SHA-256，不是明文。  
可用 PowerShell 生成：

```powershell
echo -n "你的密钥" | openssl dgst -sha256
```

## 配置说明（核心）

- 系统级：`listen_addr`、`session_cookie`、`auth_key_sha256`、`upload_dir`、`work_dir`、`backup_dir`、`deployments_file`、`log_file`。
- 程序级（`projects[]`）：`id`、`name`、`service_name`、`target_dir`、`current_version`、`max_upload_mb`、`default_replace_mode`、`backup_ignore`、`replace_ignore`。
- `service_name` 可为空：为空时部署/回滚将跳过服务启停，仅进行文件替换。

### 部署时替换策略

- 页面“程序配置”可设置程序默认 `default_replace_mode`。
- 页面“上传部署包”可按本次任务覆盖 `replace_mode`。
- 部署记录与变更明细会展示本次任务实际使用的替换模式。

## 忽略规则写法

每行一条规则，支持 `* ? []`，不支持 `**`：

- `appsettings.json`：忽略根目录文件
- `logs/`：忽略整个目录
- `*.log`：忽略任意 `.log`
- `configs/*.json`：忽略指定目录匹配文件

## 开发命令

```bash
go test ./...                  # 运行测试
gofmt -w .                     # 格式化 Go 代码
node --check web/static/app.js # 检查前端脚本语法
```

## 安全建议

- 不要提交真实密钥、生产配置和运行产物（如 `data/`、`updater.exe`）。
- 仅在受信任内网环境使用，必要时通过反向代理加 HTTPS 与访问控制。
