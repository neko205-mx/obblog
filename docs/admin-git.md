# 后台与 Git 同步

## 隔离边界

`/` 展示已发布文章列表，`/posts/<ID>` 展示完整正文；读取独立公开副本。知识库功能在需登录的 `/admin/`，登录页 `/admin/login`。发布通过单篇开关控制，功能默认可用不表示自动公开全部笔记。

默认通过 [Web 安装和设置](installation.md) 配置密码与仓库，至少 12 字节，无默认密码。密码以加盐 PBKDF2-SHA256 摘要保存，随机会话 token 的 SHA-256 和有效期只保存在内存。Cookie 为 HttpOnly、SameSite=Strict、Path=/admin/，根据 HTTPS 连接或代理的 `X-Forwarded-Proto: https` 自动使用 Secure。会话 12 小时到期，退出/重启或修改设置后失效。显式 `-vault` 仍兼容旧环境变量密码模式。会话过期不会主动跳离有草稿的编辑器。

## API

除登录外，下列接口均需有效会话。未登录 API 返回 JSON 401，工作区跳转登录页。旧根路径 `/api/...` 返回 404。

| 方法与路径 | 说明 |
| --- | --- |
| POST /admin/api/login | JSON password，设置会话 |
| POST /admin/api/logout | 撤销会话 |
| GET /admin/api/vault | 文件树、writable、saveMode（local / git） |
| GET /admin/api/note?path=... | 原始 source、渲染、revision、gitHead |
| PUT /admin/api/note | 更新：path、source、revision，Git 模式必须附 gitHead |
| POST /admin/api/note | 创建：path、source，Git 模式必须附 gitHead |
| POST /admin/api/preview | 只预览、不落盘 |
| GET /admin/api/graph | 私有关系图 |
| GET /admin/api/asset?path=... | 私有附件与范围请求 |
| GET /admin/api/git | ls-remote 检查，不 fetch / 不拉取 |
| POST /admin/api/git/pull | fetch + fast-forward，仅明确点击时执行 |
| POST /admin/api/git/push | 普通 push，不 force |
| GET /admin/api/settings | 已登录读取脱敏系统配置（Web 安装模式） |
| POST /admin/api/settings | 修改密码、仓库和选项并撤销旧会话 |

修改请求使用同源保护，响应不缓存。渲染器生成的笔记、图片和附件链接都迁入后台。

## 状态

| 状态 | 处理 |
| --- | --- |
| disabled | 普通目录，本地保存；选择 Git 仓库后自动启用同步 |
| current | 已在线验证版本一致，可以编辑，不自动拉取 |
| changed | 远端变化，手动拉取；分叉时拒绝快进，需 CLI 处理 |
| ahead | 本地提交领先，可手动推送，不继续网页编辑 |
| dirty | 在 Obsidian / CLI 核对并提交，包括未跟踪文件 |
| offline | 检查网络、凭据和 upstream，拒绝保存 |
| blocked | 分支变化或未完成 Git 操作，先用 CLI 处理 |

同步仅操作已配置的仓库，不接受任意 Git 命令。安装/系统设置允许填写经过限制的 GitHub HTTPS URL、分支和 Token 并克隆到新目录，或连接现有目录。当前分支须有初始提交和 upstream。应用不创建 GitHub 仓库，不返回可能含凭据的 Git stderr；命令有超时，使用非交互认证，不运行本地 hooks、fsmonitor 或 GPG 签名。

## 部分成功与恢复

Git 保存流程：核对远端与正文版本、原子写 Markdown、仅暂存当前文件、commit、普通 push。成功响应还含 gitHead、synced；磁盘已保存但后续 Git 失败时，synced=false 且有 warning，HTTP 仍为 200（创建 201）。前端更新 revision，持续显示警告，不把本地成功误报成远端成功，不执行回滚。

推送失败且工作树干净时可重试推送；若远端同时变化，在 CLI 手动处理分叉。暂存/提交失败时，文件及可能已有的暂存内容原样保留，由 CLI 核对。不要用强推或破坏性 reset 处理。重启后仍可从 Git 历史发现本地领先，无需进程内待推送队列。手动推送会上传当前分支的全部领先提交，不限于应用创建的提交，操作者应先确认。

服务内读写和同步互斥协调，但没有跨进程锁，不应同时用 CLI、Obsidian 或多个可写服务操作同一目录。远端检查与 push 存在竞争窗口；普通 push 拒绝非快进，失败保留本地提交。服务或浏览器断线导致响应丢失时，不能推断磁盘一定没变，请先检查正文与 Git 状态再重试。

## 测试隔离

所有 Git 写入测试使用 `t.TempDir()` 中的临时 bare remote、vault clone、第二设备 clone，不读取 OBBLOG_TEST_VAULT。浏览器验证使用 `/tmp/obblog-admin-97AMty` 和 8091 端口，覆盖登录、网页保存提交推送、远端领先阻止编辑、手动拉取并刷新数量。没有向真实笔记库或 GitHub 执行写入测试。真实 GitHub SSH / HTTPS 凭据和网络传输尚未做写入验证。
