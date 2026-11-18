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
- 必须同时存在 `obblog-<os>-<arch>`、`SHA256SUMS`、`SHA256SUMS.sig` 才标记为可更新
- 网络失败、限流、仓库无 Release 都只作为提示信息返回，不影响服务运行

GitHub 未认证 API 按 IP 限制 60 次/小时。用尽时提示「GitHub API 访问次数已用尽」，属于临时状态，稍后重试即可。

## 发布产物

打 `v*` 标签触发 `.github/workflows/release.yml`，构建并上传：

```text
obblog-linux-amd64
obblog-linux-arm64
SHA256SUMS
SHA256SUMS.sig
```

工作流在发布前会用公钥验证签名，签名或密钥缺失直接失败——**签名不是可选项**，因为更新会在服务器上执行下载的代码。

需要的仓库配置：

| 名称 | 类型 | 内容 |
| --- | --- | --- |
| `OBBLOG_SIGNING_KEY` | Secret | minisign 私钥全文 |
| `OBBLOG_SIGNING_PUBKEY` | Variable | 公钥，用于工作流自检 |

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

## 运行时布局

```text
/usr/local/bin/obblog     镜像内原始版本（只读）
/usr/local/bin/entrypoint.sh  启动器：优先 /data/bin/obblog
/data/bin/obblog          自动更新后的版本（可写卷）
```

`entrypoint.sh` 启动前会执行 `obblog -version` 校验 `/data/bin/obblog` 可运行，失败则回退到镜像内版本，避免半个二进制导致容器无法启动。

**备份时必须包含 `/data/bin`**，否则恢复后会退回镜像内的旧版本。
