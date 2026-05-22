# SimpleRemoteUpdate

一个面向 Windows 的轻量远程更新工具（单文件 `exe`），支持多程序管理、在线上传部署、回滚、版本管理、变更明细与实时日志。

## 功能概览

- 多程序独立配置（互不干扰）：`service_name`、`target_dir`、`current_version`、忽略规则等按程序保存。
- 更新流程：上传 ZIP -> 备份 -> 停服务（可选）-> 替换文件 -> 启服务（可选）-> 记录部署结果。
- 首次部署支持：当目标目录为空或不存在时，可按程序开启 `allow_initial_deploy`，首次部署会跳过备份，并可在部署完成后自动创建原生 Windows 服务或通过 NSSM 包装普通程序为服务。
- 定时维护支持：可按程序开启 `service_restart_enabled`，并通过 `service_restart_cron` 配置 cron 表达式自动重启对应 Windows 服务。
- 更新模式可选：
  - `full`（全部替换）：删除目标目录中“上传包不存在”的文件，适合完整发版。
  - `partial`（局部替换）：仅覆盖上传包内文件，不删除目标目录其他文件，适合增量发版。
- 回滚流程：基于历史备份包恢复，支持替换忽略规则。
- 实时日志：SSE 推送部署日志。
- 项目运行日志查看：自动扫描 `target_dir` 下最可能的日志目录，支持固定日志目录、尾部查看大文件、以及 `<= 50MB` 日志下载。
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
- 程序级（`projects[]`）：`id`、`name`、`service_name`、`service_restart_enabled`、`service_restart_cron`、`target_dir`、`current_version`、`max_upload_mb`、`default_replace_mode`、`allow_initial_deploy`、`service_install_mode`、`service_exe_path`、`service_args`、`service_display_name`、`service_description`、`service_start_type`、`reverse_proxy_enabled`、`reverse_proxy_bind_ip`、`reverse_proxy_rules`、`runtime_log_dir`、`backup_ignore`、`replace_ignore`。
- 系统级补充：`nssm_exe_path`（可选，指定 `nssm.exe` 路径；支持相对路径。相对路径按服务程序所在目录解析；留空则优先尝试程序目录下的 `nssm.exe`，再尝试从 PATH 查找）。
- `service_name` 可为空：为空时部署/回滚将跳过服务启停，仅进行文件替换。

### 首次部署与服务安装

- `allow_initial_deploy=true`：允许目标目录为空或不存在时直接部署；默认关闭。
- 首次部署会自动跳过备份，因此该次部署记录不能直接回滚到“部署前空目录”。
- `service_install_mode=windows_service`：仅在服务当前不存在时，部署完成后自动创建原生 Windows 服务；要求目标 EXE 自身实现 Windows 服务协议。
- `service_install_mode=nssm`：仅在服务当前不存在时，部署完成后通过 NSSM 创建 Windows 服务；适合普通 Web/控制台程序。
- `service_exe_path`：服务启动文件，通常填写压缩包解压后的 exe 文件名或相对 `target_dir` 的路径（例如 `MyApp.exe`、`bin/MyApp.exe`）；仅在极少数场景下才需要绝对路径。启用服务安装时必填。
- `service_args`：服务启动参数数组；页面上按“每行一个”编辑。
- `service_start_type`：支持 `automatic`、`manual`、`disabled`。
- `service_restart_enabled=true`：启用该程序的定时重启服务任务。
- `service_restart_cron`：cron 表达式，支持标准 5 段写法，例如 `0 * * * *`（每小时）或 `*/30 * * * *`（每 30 分钟）；也支持 `@every 30m` 这类间隔表达式。启用定时重启时必填。
- 兼容说明：历史配置里的 `service_restart_time`（`HH:MM`）仍可读取，并会在后续保存配置时自动迁移为等价的 cron 表达式。

### 内置反代

- `reverse_proxy_enabled=true`：启用项目级反代配置。
- `reverse_proxy_bind_ip`：本机监听地址；建议用 `0.0.0.0` 监听当前机器全部网卡。
- `reverse_proxy_rules`：端口映射规则数组，每条规则包含：
  - `name`：可选备注
  - `protocol`：`tcp` 或 `udp`
  - `listen_port`：当前机器对外监听的端口
  - `remote_host`：远端局域网服务器 IP/主机名
  - `remote_port`：远端服务器端口
- 保存当前程序配置后，主进程会自动启动或重启对应的反代子进程，使新规则立即生效。
- 反代由 `updater.exe` 自己托管，不依赖额外的代理服务，也不会改写业务程序的服务安装配置。

### 项目运行日志查看

- `runtime_log_dir`：可选，指定某个项目的固定日志目录，路径相对 `target_dir`，例如 `logs` 或 `runtime/logs`。
- 留空时，页面会在 `target_dir` 内自动扫描最可能的日志目录，优先识别目录名包含 `log`、`logs`、`logger`、`trace`、`runtime` 的目录。
- 仅显示常见日志文件：`.log`、`.txt`、`.out`、`.err`，以及常见 `.log.1` 轮转日志。
- 在线查看采用“最后 200/500 行 + 加载更早内容”的方式，不会整文件读入内存，适合大日志文件排障。
- 日志下载限制：文件大小 `<= 50MB` 时允许直接下载；超过 `50MB` 时仅支持在线查看。
- 所有日志目录和文件访问都限制在当前项目 `target_dir` 范围内，不能跨目录读取系统其他文件。

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
