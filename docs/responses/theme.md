# 设计令牌

一个产品通常有三套界面：服务端渲染的公开页面（[web/ui](components.md)）、用 shadcn 写的前端、用 antd 写的运营后台。品牌只在一处定义：项目的 `theme/tokens.json`，`tenon generate` 从它生成三套主题。修改一次品牌色，三套界面同时改变。

阅读前需要了解：[脚手架](../installation/scaffolding.md)；CSS 变量的基本用法。

- [令牌文件](#tokens-file)
- [生成的主题包](#generated-package)
- [服务端页面](#server-pages)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="tokens-file"></a>
## 令牌文件

`theme/tokens.json` 采用 [W3C Design Tokens](https://www.designtokens.org/tr/drafts/format/)（DTCG）格式，`tenon init` 生成的项目已经带上它。暗色的值写在每个令牌的 `$extensions` 中，键是 `tenon.dark`：

```json
{
  "color": {
    "brand": {
      "$type": "color",
      "$value": "oklch(0.511 0.262 276.966)",
      "$extensions": { "tenon.dark": "oklch(0.673 0.182 276.935)" }
    },
    "success": { "$type": "color", "$value": "#16a34a" }
  },
  "radius": { "$type": "dimension", "$value": "0.625rem" },
  "font": { "$type": "fontFamily", "$value": ["Inter", "system-ui", "sans-serif"] },
  "density": { "$type": "string", "$value": "comfortable" }
}
```

| 令牌 | 说明 |
| --- | --- |
| `color.brand` | 品牌色，必须写 |
| `color.background`、`foreground`、`muted`、`muted-foreground`、`border` | 中性色：背景、文字、弱化的背景、弱化的文字、边框 |
| `color.success`、`warning`、`error` | 语义色 |
| `color.*-foreground` | 放在品牌色、语义色上的文字颜色。不写时自动选择：白字的对比度达到 3:1 时用白字，否则用接近黑色 |
| `radius` | 圆角的基准，`px` 或 `rem` |
| `font` | 正文字体：字体名的数组或 CSS `font-family` 的值 |
| `density` | `comfortable`（默认）或 `compact` |

颜色可以写成 `#rrggbb`、`oklch(L C H)`，或者 DTCG 的对象 `{"colorSpace": "srgb" | "oklch", "components": [...]}`。只有品牌色是必须的，没有写的令牌沿用各套界面自己的默认值。令牌写错时（例如不认识的颜色名、颜色格式不对），`tenon generate` 报错并指出是哪一个；`tenon dev` 中只打印错误，改对后自动重新生成。

<a id="generated-package"></a>
## 生成的主题包

`tenon generate`、`tenon build` 和 `tenon dev` 从令牌生成前端的主题包 `packages/theme`（例如 `@todo/theme`），前端用 `workspace:*` 依赖它。`tenon dev` 运行时，修改令牌后自动重新生成，页面随之更新。

| 文件 | 内容 | 用在 |
| --- | --- | --- |
| `tokens.css` | shadcn 的 CSS 变量：`:root` 中是亮色，`.dark` 中是暗色 | shadcn 的前端和普通 CSS |
| `index.ts` 的 `cssVars` | 同样的变量，供脚本使用 | 图表等需要颜色值的地方 |
| `index.ts` 的 `antd` | antd 的令牌：`brand`（只有品牌色和圆角）、`full`（全部）、`density` | antd 的前端 |

```css
/* apps/web/src/app.css */
@import "@todo/theme/tokens.css";

button {
  background: var(--primary);
  color: var(--primary-foreground);
  border-radius: var(--radius);
}
```

令牌对应的 CSS 变量：

| 令牌 | CSS 变量 |
| --- | --- |
| `brand` | `--primary`、`--ring`、`--sidebar-primary`、`--sidebar-ring`、`--chart-1` |
| `brand-foreground` | `--primary-foreground`、`--sidebar-primary-foreground` |
| `background`、`foreground` | `--background`、`--card`、`--popover`、`--sidebar`；`--foreground` 及对应的 `*-foreground` |
| `muted`、`muted-foreground` | `--muted`、`--secondary`、`--accent`、`--sidebar-accent`；`--muted-foreground` |
| `border` | `--border`、`--input`、`--sidebar-border` |
| `error` | `--destructive`、`--destructive-foreground` |
| `success`、`warning` | `--success`、`--warning` 及对应的 `*-foreground`（shadcn 之外另加的变量） |
| `radius`、`font` | `--radius`、`--font-sans` |

antd 的令牌交给 `ConfigProvider`。antd 不接受 `oklch`，令牌中的颜色都已换算为十六进制。运营后台默认只取品牌色和圆角，其余保持 antd 的紧凑风格：

```tsx
import { antd } from "@todo/theme"
import { ConfigProvider, theme } from "antd"

<ConfigProvider
  theme={{
    token: dark ? antd.brand.dark : antd.brand.light,
    algorithm: dark ? [theme.darkAlgorithm, theme.compactAlgorithm] : theme.compactAlgorithm,
  }}
>
```

<a id="server-pages"></a>
## 服务端页面

`theme/theme.go` 把令牌嵌入程序，`theme.Tokens` 是解析后的结果。`web.Theme.WithTokens` 用它填充服务端页面的主题：圆角、字体和亮色、暗色的 CSS 变量。配置中写了的值优先，例如 `vars` 中的 `sidebar` 覆盖令牌算出的值：

```go
pages := web.New(web.Options{
    ErrorPage: ui.ErrorPage,
    Theme:     cfg.Theme.WithTokens(theme.Tokens),
})
```

`theme` 是项目自己的 `theme` 包。组件风格（`style`）、颜色模式（`mode`）仍在配置中，见[界面组件的主题](components.md#themes)。

<a id="troubleshooting"></a>
## 常见问题

**修改了 `tokens.json`，页面没有变化**

主题包没有重新生成。`tenon dev` 运行时会自动重新生成；没有运行时执行 `tenon generate`。服务端页面的主题还需要重新编译程序，因为令牌嵌入在程序中。

**`tenon generate` 报错令牌无效**

颜色格式不对或者用了不认识的颜色名。颜色写成 `#rrggbb`、`oklch(L C H)` 或 DTCG 的对象，见[令牌文件](#tokens-file)。

**运营后台只有品牌色变了，其他颜色没有变**

运营后台默认只取品牌色和圆角。需要全部令牌时，把 `antd.full` 交给 `ConfigProvider`，见[生成的主题包](#generated-package)。

<a id="api-reference"></a>
## API 参考

`github.com/go-tenon/tenon/theme`：

| 名称 | 说明 |
| --- | --- |
| `Parse(data)`、`MustParse(data)` | 解析令牌文件 |
| `Tokens.Colors` | 颜色令牌的亮色、暗色值（`Shade`），包括自动选择的前景色 |
| `Tokens.CSSVars()`、`Tokens.CSS()` | shadcn 的 CSS 变量；`tokens.css` 的内容 |
| `Tokens.Antd()` | antd 的令牌 |
| `Color.Hex()` | sRGB 的十六进制写法；超出色域的 oklch 颜色截到色域之内 |
| `web.Theme.WithTokens(tokens)` | 用令牌填充服务端页面的主题 |
