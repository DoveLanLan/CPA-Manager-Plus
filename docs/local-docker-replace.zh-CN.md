# 家里电脑本地 Docker 手动替换指南

本文用于在家里电脑上手动把本地 Docker 里的 CPA Manager / CPA Manager Plus 替换为当前项目镜像，并尽量保留原有 `/data` 数据。

当前 fork 的镜像：

```text
ghcr.io/dovelanlan/cpa-manager-plus:main
```

如果以后改回官方镜像，把文档里的镜像替换为：

```text
seakee/cpa-manager-plus:latest
```

## 1. 先确认当前容器和数据目录

先看本机现在有哪些相关容器：

```bash
docker ps -a --format 'table {{.Names}}\t{{.Image}}\t{{.Ports}}\t{{.Status}}' | grep -E 'cpa-manager|cpa-manager-plus|cli-proxy-api' || true
```

如果已经有 `cpa-manager-plus`：

```bash
docker inspect cpa-manager-plus --format '{{range .Mounts}}{{println .Type .Source "->" .Destination}}{{end}}'
```

如果旧容器叫 `cpa-manager`：

```bash
docker inspect cpa-manager --format '{{range .Mounts}}{{println .Type .Source "->" .Destination}}{{end}}'
```

重点确认哪一个 volume 或目录挂载到了容器内的 `/data`。不要误用新的空 volume，否则面板会像新安装一样没有历史数据。

常见情况：

- 旧 CPA-Manager named volume：`cpa-manager-data:/data`
- Plus 示例 named volume：`cpa-manager-plus-data:/data`
- 绑定宿主机目录：`/srv/cpa-manager-plus-data:/data` 或类似路径

## 2. 备份数据

如果 `/data` 是 named volume，例如 `cpa-manager-data`：

```bash
mkdir -p ./backup
docker run --rm \
  -v cpa-manager-data:/data:ro \
  -v "$PWD/backup":/backup \
  alpine sh -c 'cd /data && tar czf /backup/cpa-manager-data-$(date +%Y%m%d%H%M%S).tgz .'
```

如果 `/data` 是宿主机目录，例如 `/srv/cpa-manager-plus-data`：

```bash
sudo cp -a /srv/cpa-manager-plus-data /srv/cpa-manager-plus-data.backup.$(date +%Y%m%d%H%M%S)
```

## 3. 使用 compose 替换容器

建议在家里电脑固定放一个目录，例如：

```bash
mkdir -p ~/docker/cpa-manager-plus
cd ~/docker/cpa-manager-plus
```

新建 `compose.yml`：

```yaml
services:
  cpa-manager-plus:
    image: ghcr.io/dovelanlan/cpa-manager-plus:main
    container_name: cpa-manager-plus
    restart: unless-stopped
    extra_hosts:
      - "host.docker.internal:host-gateway"
    ports:
      - "18317:18317"
    environment:
      TZ: Asia/Shanghai
      HTTP_ADDR: 0.0.0.0:18317
      USAGE_DATA_DIR: /data
      USAGE_DB_PATH: /data/usage.sqlite
      CPA_MANAGER_DATA_KEY_PATH: /data/data.key
      USAGE_COLLECTOR_MODE: auto
      USAGE_RESP_QUEUE: usage
      USAGE_RESP_POP_SIDE: right
      USAGE_BATCH_SIZE: "100"
      USAGE_POLL_INTERVAL_MS: "500"
      USAGE_QUERY_LIMIT: "50000"
      USAGE_CORS_ORIGINS: "*"
    volumes:
      - cpa-manager-data:/data
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:18317/health"]
      interval: 10s
      timeout: 3s
      retries: 3

volumes:
  cpa-manager-data:
    external: true
```

这里默认复用旧 volume `cpa-manager-data`。如果你本机原来用的是 `cpa-manager-plus-data`，把两处 `cpa-manager-data` 改成 `cpa-manager-plus-data`。

如果你本机用的是宿主机目录，例如 `/srv/cpa-manager-plus-data`，把 volumes 改成：

```yaml
    volumes:
      - /srv/cpa-manager-plus-data:/data
```

同时删除文件底部的：

```yaml
volumes:
  cpa-manager-data:
    external: true
```

## 4. 停旧容器，拉新镜像，启动

如果旧容器叫 `cpa-manager-plus`：

```bash
docker stop cpa-manager-plus || true
docker rm cpa-manager-plus || true
```

如果旧容器叫 `cpa-manager`：

```bash
docker stop cpa-manager || true
docker rm cpa-manager || true
```

拉取并启动当前镜像：

```bash
docker compose pull
docker compose up -d
```

确认容器已经起来：

```bash
docker compose ps
docker inspect cpa-manager-plus --format 'image={{.Config.Image}} started={{.State.StartedAt}} status={{.State.Status}}'
```

## 5. 打开页面并验证

浏览器打开：

```text
http://localhost:18317/management.html#/
```

健康检查：

```bash
curl -sS http://localhost:18317/health
```

确认管理页 HTML 支持 gzip：

```bash
curl -sSI -H 'Accept-Encoding: gzip' http://localhost:18317/management.html | grep -i 'content-encoding'
```

如果你已经登录面板，再打开浏览器 Network 查看 `analytics` 请求，正常应该能看到响应头里有：

```text
Content-Encoding: gzip
```

## 6. CPA 地址怎么填

如果 CLIProxyAPI 跑在家里电脑宿主机上，首次 setup 里的 CPA 地址填：

```text
http://host.docker.internal:8317
```

如果 CLIProxyAPI 也在 Docker compose 同一个网络里，CPA 地址可以填容器服务名，例如：

```text
http://cli-proxy-api:8317
```

如果已经在旧数据里保存过 CPA 连接，替换容器后通常不需要重新 setup。

## 7. 回滚

如果新容器启动后有问题，先停掉：

```bash
docker compose down
```

然后把 `compose.yml` 的 image 改回旧镜像，再启动：

```bash
docker compose pull
docker compose up -d
```

如果需要恢复数据，用第 2 步的备份包恢复到原来的 volume 或目录。恢复前先停止容器，避免 SQLite 文件正在写入。

## 8. 常见问题

- 页面像新安装：大概率挂载了新的空 volume。重新检查第 1 步的 `/data` 挂载。
- 登录 key 不对：如果保留了旧 `/data`，还是用旧管理员 key。需要重置可参考 `docs/reset-admin-key.zh-CN.md`。
- 面板连不上 CPA：如果 CPA 跑在宿主机，使用 `http://host.docker.internal:8317`；Linux 下 compose 里要保留 `extra_hosts: ["host.docker.internal:host-gateway"]`。
- `analytics` 仍然下载慢：确认镜像已经更新，并确认 Network 响应头有 `Content-Encoding: gzip`。
