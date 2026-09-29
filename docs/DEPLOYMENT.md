# 部署指南

本文使用示例域名和网络地址。将它们替换为自己的地址；个人运维记录、凭据和备份保存在仓库外。

## Docker Compose

部署主机需要 Docker Engine、Compose 插件，以及设备可达的固定局域网地址或域名。

```bash
cp .env.example .env
chmod 600 .env
# 编辑 HOMEFLEET_HOST，设置自己的 HOMEFLEET_ADMIN_PASSWORD。
docker compose config -q
docker compose up -d --build
docker compose ps
```

管理员密码必须为 12–72 字节，不能使用文档占位值。首次启动写入 bcrypt 密码哈希。已有部署更改环境变量不会直接修改数据库里的密码；需要显式运行 `docker compose run --rm --no-deps hub --reset-password`，成功后旧会话全部失效。

Hub 只监听 Compose 内部网络，默认通过 Caddy 提供 HTTPS。要限制访问网卡，设置 `HOMEFLEET_BIND_IP`；443 被占用时设置 `HOMEFLEET_HTTPS_PORT`，并将带相同端口的完整地址写入 `HOMEFLEET_PUBLIC_URL`。

## 本地 CA 与证书信任

默认 Caddy 配置为指定主机名或 IP 签发本地证书。导出公钥证书：

```bash
docker compose cp caddy:/data/caddy/pki/authorities/local/root.crt ./homefleet-ca.crt
openssl x509 -in homefleet-ca.crt -noout -fingerprint -sha256
```

通过可信渠道核对指纹并把证书分发到客户端。Agent 安装使用 `--ca` / `-CA`，浏览器还需导入系统证书存储。不要分发 Caddy CA 私钥，不要关闭 TLS 校验。

## 复用已有反向代理

已有 Traefik 提供 `websecure` 入口、受信任域名证书和共享 Docker 网络时，在 `.env` 配置：

```dotenv
COMPOSE_FILE=compose.yaml:compose.traefik.yaml
HOMEFLEET_DOMAIN=homefleet.example.com
HOMEFLEET_PROXY_NETWORK=proxy
HOMEFLEET_BIND_IP=127.0.0.1
HOMEFLEET_HTTPS_PORT=8443
```

`proxy` 必须替换为已有代理网络名。该覆盖文件设置正式 URL 和 `HOMEFLEET_CUSTOM_CA=false`，一键安装默认使用域名并省略本地 CA 参数。证书及 DNS challenge 凭据由原代理管理，不复制到 HomeFleet。

域名需要解析到客户端可以访问的代理地址；拥有公网信任证书不要求开放公网入站端口。可使用内网 DNS 或 VPN。

## 多个接入地址

`HOMEFLEET_ALLOWED_ORIGINS` 接受逗号分隔的完整 HTTPS 来源，例如域名及 VPN 的备用地址。每个地址都必须有实际监听端口、可达路由和匹配的证书；只添加 Origin 不会自动配置网络或签发证书。

候选入口会显示在接入窗口，已有 Agent 的网卡信息用于补充网卡名。选择入口后，下载链接、命令与 CA 要求同步更新。自定义地址支持 IPv4、方括号包裹的 IPv6 和非默认端口；不支持附带账号、路径、查询参数或 fragment。

更换已接入设备的 Hub 地址时，保留 `device_id`、`token` 和执行记录，只调整配置中的 `hub_url` 及必要的 `ca_cert`，然后在没有修改事务时重启该 Agent。重新运行安装器会保留已有设备配置。

## 离线构建

在可信构建机上生成前端、Hub、Agent 和许可证文件，再传到部署主机：

```bash
bash scripts/build.sh
mkdir -p prebuilt
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
  -ldflags="-s -w -X github.com/xiaoliu-heng/homefleet/internal/model.Version=$(cat VERSION)" \
  -o prebuilt/homefleet-hub ./cmd/hub
cp -R web/dist prebuilt/web
cp -R dist/releases prebuilt/releases
cp deploy/Dockerfile.prebuilt prebuilt/Dockerfile
```

ARM64 部署主机将 Hub 的 `GOARCH` 改为 `arm64`。单独传输并加载 Dockerfile 使用的官方基础镜像。增加只在本机使用的 `compose.override.yaml`：

```yaml
services:
  hub:
    build:
      context: ./prebuilt
      dockerfile: Dockerfile
```

然后运行 `docker compose build hub` 和 `docker compose up -d --no-deps --no-build --pull never --wait hub`。每次源码更新都需要重新生成构建产物。保留旧的 `releases/agents/<版本>/`，避免破坏已批准的固定版本任务。

## 运维、备份和恢复

```bash
docker compose ps
docker compose logs --tail 100 hub caddy
bash scripts/backup.sh /absolute/backup/homefleet-YYYYMMDD
```

备份包含 SQLite、加密主密钥、部署环境、Compose 覆盖文件及 Caddy CA。存放于仓库外，限制权限；外部反向代理及 DNS 需要独立备份。

恢复通过 `scripts/restore.sh BACKUP --confirm-replace` 显式替换数据卷。核对设备身份和备份后的任务结果，保留 Agent 本地执行记录；无法确定结果时禁止自动重跑。做隔离恢复演练时不要接入正式代理网络或注册相同域名路由。

Agent 更新需在控制台预览并手动确认。发布新 Hub 或新 Agent 文件不会自动更新设备，详见 [AGENT-UPDATES.md](AGENT-UPDATES.md)。
