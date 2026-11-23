# 博客主题

主题控制公开博客的首页、文章页和独立页面，不改变后台界面、已发布内容或公开地址。主题使用 Go `html/template` 和 CSS，不需要编译步骤。Markdown 和数学公式仍由 OB Vault 渲染。

## 安装与切换

只安装你自己编写或审查过的可信主题目录。后台不提供主题上传。

Podman 部署使用现有的 `/data` 数据卷。可以将示例主题目录复制到正在运行的 `obblog` 容器中：

```sh
podman exec obblog mkdir -p /data/themes
podman cp examples/themes/plain-journal obblog:/data/themes/
```

复制后清单应位于 `/data/themes/plain-journal/theme.json`，而不是 `/data/themes/theme.json`。确保应用运行用户可读。直接运行二进制时，主题目录位于 `-data` 指定的目录下，即 `<data>/themes/`。

1. 登录后台，打开「设置与工具 → 外观」中的博客主题区域。
2. 选择主题，在新标签页预览首页；已有公开文章或独立页面时，也可以直接预览对应页面。预览不会改变当前启用的主题，预览中的普通链接仍指向正式公开地址。
3. 确认后启用。启用状态按知识库保存，主题文件则放在数据目录中。复用同一个 `/data` 卷替换容器时，两者都会保留。

无法解析或加载的主题不能启用。如果当前主题被移除或变得无效，公开站点回退到内置默认主题。自制主题调试时，分别检查首页、文章、独立页面以及浅色和深色模式。

## 主题目录

[Plain Journal](<../examples/themes/plain-journal/theme.json>) 是一套可直接改写的示例。目录名是主题 ID，例如 `plain-journal`。编写自己的主题时请用不同的 ID；可使用小写字母、数字和连字符，以字母或数字开头，不能使用保留 ID `default`。

```text
plain-journal/
  theme.json
  index.html
  post.html
  page.html
  partials/
    chrome.html
  assets/
    style.css
```

`theme.json` 包含展示名称、主题自身的版本和说明；`apiVersion` 是模板接口版本，当前为数字 `1`。

```json
{
  "name": "Plain Journal",
  "version": "1.0.0",
  "description": "简洁的文字博客主题",
  "apiVersion": 1
}
```

三个根模板都是必需的：`index.html` 渲染 `/`，`post.html` 渲染 `/posts/<ID>`，`page.html` 渲染 `/pages/<短地址>`。将每种页面的完整 HTML 写在对应文件中。共用的部件可以写在 `partials/` 下的平级 `.html` 文件里，用 `{{define "部件名"}}…{{end}}` 定义，并通过 `{{template "部件名" .}}` 传入当前页面数据。部件名应在主题内唯一。

## 模板接口

| 字段 | 用法 |
| --- | --- |
| `.Site` | `Title`、`Description`、`Author`、`Footer`、`Mode`、`Accent`，以及由 `{Title, URL}` 条目组成的 `Navigation`。用 `Navigation` 显示独立页面等站点链接，不要自行从笔记路径拼接地址。 |
| `.Posts` | 首页的已发布文章列表；`{{range .Posts}}` 后，当前数据是一个文章条目。可用 `{{else}}` 提供空列表提示。 |
| `.Post` | 文章页的当前文章。文章条目提供 `Title`、`URL`、`Excerpt`、`PublishedAt`、`UpdatedAt`、`ShowUpdated` 和 `TitleID`。用 `ShowUpdated` 控制是否显示更新时间；`TitleID` 非空时可用作文章标题的锚点。 |
| `.Page` | 独立页面的 `Title`、`URL`、`UpdatedAt`。独立页面不在 `.Posts` 中。 |
| `.Content` | 文章页或独立页面的安全 HTML 正文，直接写 `{{.Content}}` 即可。主题无需转换 Markdown。 |
| `.Headings` | 正文标题列表，条目包含 `ID`、`Text` 和 `Level`，可用来生成目录。 |
| `.ThemeAttr` | 根据站点设置生成的根元素主题属性，使用 `<html lang="zh-CN"{{.ThemeAttr}}>`。无需自己拼 `data-theme`。 |

可用函数：`asset` 根据 `assets/` 内的相对路径生成资源 URL；`date` 格式化面向读者的日期；`isoDate` 生成适合 `<time datetime>` 的日期时间。不要把 `assets/` 前缀传给 `asset`，也不要写死主题资源 URL。

```html
<link rel="stylesheet" href="/theme.css">
<link rel="stylesheet" href="{{asset "style.css"}}">
```

`/theme.css` 是可选的共享配色样式，示例通过它使用站点的主题色和深浅设置。主题自己的 CSS、位图和字体放在 `assets/` 中；例如 PNG、JPEG、WebP 或 WOFF2。从 CSS 引用同目录资源时，可写相对路径，例如 `url("cover.webp")`。不支持 SVG、HTML 或 JavaScript 资源。

主题只负责展示公开模型。不要添加 JavaScript、内联事件处理器、PHP 或其他可执行代码。模板没有读取私有笔记、配置或 Git 凭据的接口。主题不接管附件路由；正文中已经生成的文章、独立页面和公开附件链接应原样保留。

## 归档和文章标签

主题可用 `<a href="{{.Site.ArchiveURL}}">归档</a>` 添加入口。文章页的 `.Post.Tags`、`.Post.Categories` 提供 `Name` 和 `URL`；`.Post.Words` 可显示估算字数。

可选的根模板 `archive.html` 覆盖 `/archives`；不提供则使用内置归档。模板可访问 `.Archive.Stats`（`Categories`、`Posts`、`Tags`、`Words`）、`.Archive.Tags`、`.Archive.Categories`（`Name`、`URL`、`Count`、`Selected`），以及 `.Archive.Years`（`Year`、`Open`、`Posts`）。归档文章另提供北京时间的 `.DateShort`（`MM-DD`）。筛选后的 `.Archive.Matches` 不改变全站统计。通过条目 URL 添加或移除筛选；自定义主题继续禁用脚本和表单提交。

## 给独立页面提供模板

在 `theme.json` 可选的 `pageTemplates` 数组中声明模板（最多 16 个）。例如：

```json
"pageTemplates": [
  {"id":"profile","label":"个人简介","description":"简洁的个人页",
   "title":"关于我","slug":"about","format":"markdown","style":"profile.css"}
]
```

- `id` 是主题内唯一的短标识，只能用小写字母、数字和连字符；保存到页面的完整标识是 `主题ID/模板ID`。`label` 必填，`title` 和 `slug` 省略时分别使用 `label` 和 `id`。
- `page-templates/<id>.html` 是必需的 Go HTML 正文片段，只写正文区域，不写完整 HTML 文档。它可以使用 `.Content`、`.Page`（含 `.Template`）、`.Headings`、`.Site` 等公开字段。主题自己的 `page.html` 仍负责全页外壳，原样输出 `.Content` 即可。
- `page-templates/<id>.md` 是可选的起始 Markdown，建页时复制到草稿（不超过 32 KiB）。`style` 可选，指向 `assets/` 内的 CSS 文件；编写选择器时使用自己的命名空间，以免影响页面外壳。
- `format` 默认 `markdown`。使用 `friend-links` 时，`.Content` 中符合约定的链接会变成内置卡片，模板也可使用 `.FriendLinks` 自定义呈现，其字段为 `Title`、`URL`、`Description`、`Avatar` 和 `Initial`。友链写法见[发布文档](publishing.md)。

可参考 Plain Journal 的[简介模板](../examples/themes/plain-journal/page-templates/profile.html)。只有当前主题的模板会出现在新建选项里，内置友链模板始终可用。后台在禁用脚本的隔离框中预览自定义片段；主题切换、失效或模板被移除时，已有页面回退到普通 Markdown，原正文和模板标识均保留。
