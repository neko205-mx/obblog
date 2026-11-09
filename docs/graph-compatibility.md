# Obsidian 关系图谱：兼容设计与验证

## 研究结论

兼容目标是文件和链接的语义，而不是复刻 Obsidian 内部绘图引擎或某次布局坐标。

官方资料将图谱定义为笔记节点及其内部链接，并提供全库和可调深度的局部视图；节点大小与引用它的笔记数量相关。图谱可过滤标签、附件、未创建笔记和孤立笔记。依据：[Graph view](https://obsidian.md/help/plugins/graph)。

公开 API 的 `MetadataCache.resolvedLinks` 和 `unresolvedLinks` 都是「源文件 -> 目标 -> 引用次数」索引；`getFirstLinkpathDest` 根据链接和源路径寻找文件，`CachedMetadata` 还包括正文链接、嵌入与属性链接。我们的数据模型据此设计，不声称调用或复用 Obsidian 的内部引擎。依据：[官方 API 声明](https://github.com/obsidianmd/obsidian-api/blob/master/obsidian.d.ts)。

Wikilink、Markdown 链接、别名、章节和块定位的路径部分，都可以归一到文件节点；Mermaid 内的链接不应加入笔记关系图。依据：[Internal links](https://obsidian.md/help/links)、[Advanced formatting syntax](https://obsidian.md/help/advanced-syntax)。

## 实现

```text
本地目录（只读）
  -> vault.Scan：文件快照
  -> markdown.Extract：Markdown AST / YAML 属性
  -> vault.Resolve / ResolveWiki：共享链接解析
  -> graph.Build：节点、方向、引用次数、歧义和失败提示
  -> GET /api/graph：稳定排序的 JSON
  -> selectGraph：类型/搜索筛选、入链与出链 BFS
  -> d3-force + Canvas：力布局和交互
```

后端用 Go 建索引，前端使用打包在本地的 d3-force 3.0.0 做数值模拟，Canvas 绘图。选择 D3 是本项目的工程决定，不是对 Obsidian 内部实现的判断。D3 支持布局与绘图系统解耦，包含排斥、连线、位置与碰撞力。依据：[d3-force 官方文档](https://d3js.org/d3-force)。

现有笔记、附件使用 `file:<仓库相对路径>` 作为稳定 ID；未解析目标使用 `unresolved:<路径>`，标签使用 `tag:<标签>`，避免不同类型 ID 冲突。相同源与目标只生成一条有向边，`count` 保存出现次数。节点大小依据当前可见图中不同来源的入链数量；入链、出链和邻居数量不等同于重复引用次数。

章节和块引用只影响阅读定位，不生成额外节点；同页章节链接和显式自引用不生成自环。嵌入引用参与索引，但正文嵌入渲染仍有上一版的限制。

关系索引和阅读跳转共用同一解析器。正文按 AST 识别，代码块、行内代码、原始 HTML 不作链接解析；Obsidian 注释在保留源偏移和换行的情况下遮蔽，不更改文件。YAML 属性使用结构化解析，读取双链和标签，不把属性键或别名列表作为正文引用。

局部图先应用类型和搜索过滤，再对可见边进行无向 BFS，所以出链和入链邻居均可进入视图，深度限制为 1-3。方向信息仍保留，用于可选的连线箭头。

图谱只使用服务启动时指定的目录，不合并其他目录。接口本身只读，现已迁至需登录的 `/admin/api/graph`。编辑见 [编辑说明](editing.md)，后台与 Git 同步见 [后台说明](admin-git.md)，保存或拉取后使图谱缓存失效。

## 兼容范围与差异

| 项目 | 当前行为 |
| --- | --- |
| 正文双链 / Markdown 相对链接 | 支持，含中文、空格和 URL 编码路径 |
| 属性双链 / 标签 | 支持结构化 YAML、正文标签、嵌套标签 |
| 未创建笔记 | 保留独立节点，可隐藏，点击不会创建文件 |
| 附件 / 孤立笔记 | 可独立过滤；默认附件隐藏、孤立笔记显示 |
| 章节 / 块引用 | 聚合到所属文件；块定位阅读尚未实现 |
| 代码 / Mermaid / 注释 | 不计入关系连线，不执行其中的内容 |
| 全库 / 局部图谱 | 支持全库及 1-3 层邻居 |
| 节点交互 | 拖动、平移、缩放、悬停高亮、点击阅读、键盘节点选择器 |
| 同名歧义 | 明确提示，不复刻未公开的最佳匹配决策 |
| 路径大小写 | 大小写敏感，可能与某些客户端平台不同 |
| 搜索语言 | 只支持子串、path:、tag: 和 #标签，不支持完整布尔/正则搜索 |
| 分组着色 | 提供一级目录着色，不支持任意搜索表达式颜色组 |
| 客户端配置 | 不导入 `.obsidian/graph.json`、排除规则、配色和力参数 |
| 其他格式 | 不索引 Canvas、Bases、未支持的附件类型或插件运行结果 |
| 增量 / 超大图 | 按需全量索引，浏览器主线程布局；尚无增量索引或 Worker |

没有使用 Obsidian 客户端导出的完整 `resolvedLinks` 逐边比对，因此这里的验证不等于「所有 Obsidian 边缘语法完全一致」。后续若需要严格等价，可在用户本机导出该索引作为只读比对基准。

## 真实仓库验证

验证日期：2026-11-07。仓库：[neko205-mx/obsidian-vault](https://github.com/neko205-mx/obsidian-vault)。固定提交：`d142ec42f0c97e6a9a1e122faa28147e5144b795`。

真实内容只克隆到被忽略的 `.local/obsidian-vault`。源码与自动化夹具不包含笔记正文、附件或完整关系索引。测试以本地路径显式启用，测试前后校验笔记 SHA-256 和文件快照，并通过 Git 状态确认克隆目录没有修改。

本次扫描得到 164 篇笔记、116 个支持的附件、1 个标签节点和 2 个未解析目标。全部类型共有 283 个节点、162 条聚合有向边。默认隐藏附件和标签后是 166 个节点、63 条边，其中 94 个孤立节点；隐藏孤立节点后是 72 个节点、63 条边。测试日志仅输出统计，不输出正文。

发现 1 份文档的 YAML 属性无效，界面保留可展开提示，跳过该文档的属性关系，但继续索引正文。未修复或改写用户原文件。

普通本机 HTTP 索引请求本次约 64ms；开启 Go 竞态检测并包含测试校验的集成测试约 0.3s。这是一次运行观察，不是稳定性能承诺。

自动测试覆盖重复链接次数、章节/块归一、同页引用、短链和完整路径、属性引用、编码路径、代码/注释忽略、图谱确定性、取消请求、筛选后的孤立度计算，以及入链和出链邻居深度。真实库 164 篇笔记的 HTTP 阅读接口全部通过，返回的 Markdown 与源文件逐字一致。浏览器人工验证了图谱打开、节点定位和阅读、局部图、节点拖动、目录筛选、孤立节点开关、缩放、深浅主题和历史返回。

## 测试与依赖重建

运行 Go 服务不需要 Node.js。以下客户端单测和力库重建是开发时可选操作：

```bash
go test -race ./...
go vet ./...
go build ./...
node --test tests/graph-model.test.mjs
OBBLOG_TEST_VAULT="$PWD/.local/obsidian-vault" go test -race -v ./internal/graph
```

力库的生成入口是 `scripts/force-entry.mjs`，许可证在 `scripts/d3-license.txt` 并打入最终 JS 文件。当前固定版本：d3-force 3.0.0，d3-dispatch / d3-quadtree / d3-timer 3.0.2，构建器 esbuild 0.25.12。

```bash
npm install --prefix /tmp/obblog-graph-build --no-audit --no-fund d3-force@3.0.0 d3-dispatch@3.0.2 d3-quadtree@3.0.2 d3-timer@3.0.2 esbuild@0.25.12
env NODE_PATH=/tmp/obblog-graph-build/node_modules /tmp/obblog-graph-build/node_modules/.bin/esbuild scripts/force-entry.mjs --bundle --format=esm --minify --outfile=internal/web/static/force.js --banner:js="$(< scripts/d3-license.txt)"
```
