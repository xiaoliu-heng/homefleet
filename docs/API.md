# API v1

修改请求使用 JSON。管理 API 需要管理员会话 cookie；POST/PATCH 还需 `X-HomeFleet-Request: 1`，浏览器 Origin 必须等于 `HOMEFLEET_PUBLIC_URL`，或精确匹配 `HOMEFLEET_ALLOWED_ORIGINS` 中显式配置的完整 HTTPS 来源。Agent 使用独立 bearer token，不能访问管理接口。

## 管理接口

| 方法与路径 | 内容 |
|---|---|
| POST /api/v1/login | `{password}`，设置 HttpOnly / SameSite=Strict cookie，生产模式 Secure |
| POST /api/v1/logout | 注销当前会话 |
| GET /api/v1/me | 管理员与 public_url |
| GET /api/v1/devices | 已登记设备、能力、最后指标 |
| GET /api/v1/devices/{id} | 单台设备 |
| PATCH /api/v1/devices/{id} | `name, group, management_url` |
| POST /api/v1/devices/{id}/revoke | 撤销凭据、取消排队目标 |
| GET /api/v1/devices/{id}/metrics | 7 天数据，响应约 720 个采样点 |
| GET /api/v1/devices/{id}/inventory | 上次清单和错误 |
| GET /api/v1/enrollment-addresses | 可选接入地址、默认 URL、网卡名及 CA 要求；仅返回配置的 HTTPS 入口，需要管理员登录 |
| POST /api/v1/enrollment | 单次令牌、过期时间、`hub_url`、`requires_ca`；公开受信任证书模式固定使用正式域名且无需 CA 文件 |
| POST /api/v1/appliances | `name, group, management_url, probe:{type,target}` |
| GET /api/v1/catalog | 软件目录映射 |
| GET /api/v1/projects | 项目模板，机密只返回 key |
| POST /api/v1/projects | 新建/更新模板；传 id 更新并增加版本 |
| POST /api/v1/jobs/preview | `{device_ids:[],action:{…}}` |
| POST /api/v1/jobs/{id}/execute | `{device_ids:[]}`，确认已通过检查的子集 |
| POST /api/v1/jobs/{id}/cancel | 取消排队目标/后续步骤 |
| POST /api/v1/jobs/{id}/retry | 仅失败/被阻止目标，新建预检查 |
| GET /api/v1/jobs | 最近 100 个任务 |
| GET /api/v1/jobs/{id} | 逐设备计划和结果 |
| GET /api/v1/targets/{id}/logs?after=N | 增量日志，最多 2000 条 |
| POST /api/v1/targets/{id}/resolve | `{state:"succeeded"或"failed",note:"核实记录"}` |
| GET /api/v1/events | SSE 变化通知；客户端获取最新状态/增量日志 |
| GET /api/v1/downloads/{name} | Agent、安装脚本、SHA256SUMS |
| GET /install.sh | 无需登录的 Linux/macOS 一键安装引导脚本 |
| GET /install.ps1 | 无需登录的 Windows 一键安装引导脚本 |
| GET /downloads/{name} | 无需登录的发布文件白名单；Agent、bootstrap/install/uninstall 脚本、SHA256SUMS、LICENSE、THIRD_PARTY_NOTICES.md；不提供配置或凭据 |
| GET /healthz | 无认证健康检查 |

软件 action：`kind=package`，`operation=install|upgrade|upgrade_all`，`catalog_id` 或 `package`，Windows `scope=machine|user`。

Arch 安装需要 Agent 0.2.2 起上报的 `capabilities.pacman_cached_install.available`；旧 Agent 的安装预览被阻止，提示先手动更新 Agent，避免旧版仍执行全量升级。`install` 使用已有索引的 `pacman -S --needed`，预览和执行前均通过 `pacman -Qu` 检查是否已存在待升级包；`upgrade` / `upgrade_all` 仍使用 `pacman -Syu`。新版 Agent 不执行旧版预览生成的全量升级安装计划，要求重新预览。

项目 action：`kind=project, operation=deploy, project_id`。

Compose action：`kind=compose, operation=status|logs|start|stop|update, project_id`。

清单 action：`kind=inventory, operation=refresh, scope`，创建只读 inspect 任务，不需要第二次确认。

接入命令允许在浏览器中替换下载/连接地址，不会修改服务器的 public_url、Origin 白名单或已有 Agent 配置。新地址必须实际连接同一 Hub，并提供与该域名/IP 匹配的 HTTPS 证书。公开下载无需令牌；创建接入令牌仍要求管理员登录，Agent 注册仍消耗一次性令牌。

项目字段：`name, repository, ref, directory`（后备目录）、`directories:{linux,darwin,windows}`、`platforms:{OS:{steps:[],health_check}}`、`env`、`secret_env`、`compose_file`。更新时未提供的机密保留，空值表示删除。

## Agent 接口

| 方法与路径 | 内容 |
|---|---|
| POST /agent/v1/register | `{token,device}` → 稳定 id 和独立 token |
| POST /agent/v1/heartbeat | `{device,sample}`，服务端时钟决定在线状态 |
| POST /agent/v1/reconcile | `{known:[targetID,…]}`，启动核对本地记录 |
| GET /agent/v1/claim | 长轮询；无任务 204，有任务返回 assignment |
| GET /agent/v1/targets/{id}/control | 取消标志和任务状态 |
| POST /agent/v1/targets/{id}/logs | `[{seq,at,stream,text},…]`，相同 target/seq 去重 |
| POST /agent/v1/targets/{id}/result | `{state,reason,plan?,inventory?,exit_code,commit?,health?}` |

assignment 包含固定目标、job ID、mode、action 快照及批准 plan。仅本机任务可写入结果和日志；机密仅在该 Agent 领取任务时解密。

## 执行与恢复

1. 预览返回命令、身份、目录、范围和 5 分钟有效期。提交后的任务不因排队超过 5 分钟失效，执行前再次检查本地条件。
2. 同一 preview 重复提交返回同一 execution，不通过重复提交扩大范围。
3. 服务端持久化领取状态；Agent 原子持久化本地记录后执行。一个数据目录仅允许一个 Agent 进程。
4. 重复 target 只补传日志/结果，不重跑命令。重启时未完成的修改任务标为 unknown。
5. 离线后执行目标标为 unknown；允许同一次尝试补传真实结果。未知结果阻止该设备继续修改，直到核实。
6. 日志按 seq 续传；SSE 仅为变化通知。Agent 单任务日志最多 8 MiB，单行约 60 KiB。
7. 包管理事务不因取消/断网被强杀；其他步骤限时 30 分钟，超时可能留下子进程，因此标为 unknown。失败不自动回滚。

Hub 仅支持单实例调度，多 Hub 共用 SQLite 不在首版范围。指标保留 7 天，任务审计与软件清单持续保留。

## Agent 更新（0.2.0 起）

- `GET /api/v1/agent-release`（管理员）：`{release: {version, minimum_version, published_at, artifacts}, reason?}`；没有完整发布包时 `release` 为 null。
- `GET /downloads/agent-release.json`：公开发布清单，不包含凭据。
- `GET /downloads/agents/{version}/{name}`：仅允许稳定 X.Y.Z 与四平台文件名；保留旧版本供已批准任务下载。
- `POST /api/v1/jobs/preview`：`action={kind:"agent", operation:"self_update", agent_version:"0.2.0"}`，附固定 `device_ids`。服务器忽略客户端发布清单，使用本地验证的版本快照。
- 继续使用现有 `/jobs/{id}/execute`、日志、取消和结果 API。`plan.agent_update` 包含 `from_version/version/artifact`；结果包含 `agent_version` 和回连健康状态。
- 设备的 `agent_version` 为二进制内置版本，`capabilities.agent_update` 表示是否具备后台更新条件。旧版无此能力时服务端直接阻止投递。
