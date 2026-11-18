# 版本与更新

## 版本号

版本在构建期注入二进制，运行时可查：

```bash
obblog -version          # v1.2.3 (abcdef1)
```

后台「设置与工具 → 工具」显示当前版本，并可检查 GitHub 上是否有更新。`GET /admin/api/version` 返回 `{"version","commit","date"}`。

未注入时报告 `dev`，它的排序低于任何已发布版本，因此开发构建永远能看到正式版为「有更新」。

## 检查更新

`GET /admin/api/update` 请求 `https://api.github.com/repos/neko205-mx/obblog/releases/latest`。

判定规则：

- 只比较数字部分，`v1.2.3-rc1` 视为 `1.2.3`，正式安装不会收到预发布版
- 草稿和预发布版本一律忽略
- 必须同时存在 `obblog-<os>-<arch>` 和 `SHA256SUMS` 才标记为可更新
- 网络失败、限流、仓库无 Release 都只作为提示信息返回，不影响服务运行

GitHub 未认证 API 按 IP 限制 60 次/小时。用尽时提示「GitHub API 访问次数已用尽」，属于临时状态，稍后重试即可。

## 发布产物

打 `v*` 标签触发 `.github/workflows/release.yml`，构建并上传：

```text
obblog-linux-amd64
obblog-linux-arm64
SHA256SUMS
```

**当前不做签名。** 校验和只能发现传输损坏或截断，**不能证明文件来源**——如果攻击者能篡改 GitHub 上的文件，他也能同时改掉 `SHA256SUMS`，校验依然通过。

这是有意接受的取舍：更新源通过配置信任，因此需要确保发布仓库（`neko205-mx/obblog`）的账号安全。如果以后要分发给不受控的第三方，应改为签名校验。

不需要任何仓库 Secret 或 Variable。

本地手动发布：

```bash
podman build --build-arg VERSION=v1.2.3 \
  --build-arg COMMIT=$(git rev-parse HEAD) \
  --build-arg BUILD_DATE=$(date -u +%FT%TZ) \
  -t localhost/obblog:dev .
```

## 更新为何替换整个二进制

前端资源（HTML/CSS/JS）通过 `//go:embed` 编译进二进制，因此**替换二进制即完成全部升级**，包括界面改动。

这样做换来的是版本一致性：前端代码与后端 API 永远来自同一次构建，不会出现「新 JS 调旧接口」。代价是改一行 CSS 也要走完整更新流程，但 15 MB 的传输和 1–2 秒重启在可接受范围内。

需要频繁调整的展示内容已经配置化（博客标题、色板、独立页面存在 `publications.json`），改这些不需要更新代码。

## 安装更新

默认**只检查不安装**。「自动安装更新」开关（`update.autoInstall`）默认关闭，且它只影响面板文案，不会在后台自动执行——安装始终需要点击「立即更新」。

这是刻意的：安装会替换正在运行的服务端二进制，失败可能导致服务不可用，因此做成显式操作而非后台行为。

点击「立即更新」后依次执行：

1. 请求 `GET /admin/api/update/install`
2. 下载 `SHA256SUMS`，取出本平台二进制的摘要
3. 下载二进制到 `<data>/bin/.download-*`，边写边算 SHA-256
4. 比对摘要，不符则删除临时文件并拒绝
5. 校验 ELF 头与架构，防止下错平台
6. `chmod 0755` 后原子 `rename` 到 `<data>/bin/obblog`
7. 请求 `POST /admin/api/update/restart`

重启使用 `syscall.Exec` 替换进程映像，PID 不变，因此容器不需要重建，健康检查持续有效。前端在重启后轮询 `/admin/api/version`，恢复即自动刷新页面，30 秒未恢复则提示检查日志。

**任何一步失败都不会留下半成品**：临时文件在失败路径上被删除，`<data>/bin/obblog` 只在全部校验通过后才被替换。

### 安装目录

**安装目录跟随 `-data`，不是硬编码 `/data`**。`-data /v/data` 时写到 `/v/data/bin/obblog`。这与 entrypoint 的解析逻辑一致，容器重启后仍能选中新版本。

## 运行时布局

```text
/usr/local/bin/obblog     镜像内原始版本（只读）
/usr/local/bin/entrypoint.sh  启动器：优先 /data/bin/obblog
/data/bin/obblog          自动更新后的版本（可写卷）
```

`entrypoint.sh` 启动前会执行 `obblog -version` 校验 `/data/bin/obblog` 可运行，失败则回退到镜像内版本，避免半个二进制导致容器无法启动。

**备份时必须包含 `/data/bin`**，否则恢复后会退回镜像内的旧版本。
