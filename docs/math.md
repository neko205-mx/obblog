# 数学公式

## 语法

行内公式用 `$...$`，块级公式用 `$$...$$`：

```markdown
质能方程 $E = mc^2$。

$$
\frac{-b \pm \sqrt{b^2 - 4ac}}{2a}
$$
```

块级公式可以写成单行 `$$a^2+b^2=c^2$$`，也可以跨行。代码块和行内代码里的 `$` 不会被解析。

## 支持的语法

| 类别 | 命令 |
| --- | --- |
| 上下标 | `x^2`、`x_i`、`x_i^2`、`x^{2n}` |
| 分数 | `\frac{a}{b}`、`\dfrac`、`\tfrac`、`\binom{n}{k}` |
| 根号 | `\sqrt{x}`、`\sqrt[3]{x}` |
| 希腊字母 | `\alpha`…`\omega`、`\Gamma`…`\Omega` |
| 运算符 | `\times` `\cdot` `\pm` `\div` `\leq` `\geq` `\neq` `\approx` `\in` `\to` `\Rightarrow` 等 |
| 大型运算符 | `\sum` `\prod` `\int` `\oint` `\bigcup`（上下限堆叠） |
| 括号 | `\left( ... \right)`，支持嵌套与 `\left.` 隐形括号 |
| 字体 | `\mathbf` `\mathit` `\mathrm` `\mathbb{R}` `\mathcal{L}` `\mathfrak{g}` |
| 文字 | `\text{中文说明}` |
| 矩阵 | `\begin{matrix}` `\begin{pmatrix}` `\begin{bmatrix}` |
| 修饰 | `\overline` `\underline` `\bar` |
| 间距 | `\,` `\:` `\;` `\!` `\quad` `\qquad` |

## 渲染方式

服务端把 LaTeX 转换成 **MathML**，浏览器原生排版。这样不需要 CDN、不执行外部脚本，符合页面的内容安全策略（CSP），也和「前端资源编译进二进制」的设计一致。

解析由 [`github.com/go-latex/latex`](https://github.com/go-latex/latex) 完成，本项目的 `internal/math` 只负责它缺失的部分：AST 到 MathML 的转换，以及库不支持语法的预处理。

库没有 MathML 输出，且遇到未知宏会 panic 而非返回错误，所以每次调用都经过 `recover` 包装。

## 已知限制

**上标在前、下标在后需要加花括号。** `x^2_i` 会被解析成上标 `2_`，这是上游解析器的词法缺陷。写成 `x^{2}_{i}` 即可。

**不支持的语法会原样显示。** 未知命令输出为等宽文本（如 `\notacommand`），而不是让整条公式失败。公式本身语法错误时（花括号不配对、重复上标），整条显示为红色错误块并保留源码，便于定位。

**未实现**：`\begin{align}` 等多行环境、`\overbrace`/`\underbrace`、化学式、`\tag`。

## 与文章的配合

公式不参与关系图谱索引：`[[链接]]` 或 `#标签` 写在公式里不会被当作真正的链接或标签。

公开博客使用同一渲染器，因此发布文章的公式与后台阅读效果一致，也不需要额外的公开资源。
