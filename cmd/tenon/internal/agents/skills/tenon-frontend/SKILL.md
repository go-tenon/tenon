---
name: tenon-frontend
description: 在 Tenon 应用的前端（apps/<前端>，TanStack Start + Mantine + @go-tenon/ui）中新增或修改页面：文件路由与菜单、调用生成的客户端和 TanStack Query、列表与表单、按权限显示、颜色与样式。写 React 页面、表格、表单、菜单时使用。
---

# 前端页面

每个前端是 `apps/` 下的一个 TanStack Start 项目，界面用 Mantine 和 `@go-tenon/ui`，调用接口用 `bun run generate` 生成的客户端。先读 `.tenon/docs/frontend/conventions.md`，组件的用法查 Mantine 的文档（Mantine 给 AI 的文档和 MCP 服务按 `README.md` 中的方法接入）。TanStack、Mantine 的官方技能与本技能冲突时，以本技能和 `AGENTS.md` 为准，例如前端不写 TanStack Start 的服务端函数，业务逻辑都在 Go 的接口中。

## 步骤

1. **先有接口**：页面要的数据和操作先在 Go 中写成接口（技能 `tenon-http-api`），运行 `bun run generate`。前端从 `@<项目>/client/<API 名称>` 导入生成的类型和函数，不手写请求。
2. **新建页面**：`apps/<前端>/src/routes/` 中一个文件一个页面，用 `createFileRoute`。需要登录的后台页面放在布局路由 `_app` 下，`staticData` 的 `title`、`icon`、`order` 决定菜单，见 `.tenon/docs/frontend/app.md#route-metadata`、`.tenon/docs/frontend/layout.md#menu`。
3. **读数据和修改**：用生成的查询选项，`useQuery(queries.article.get(id))`、`useMutation(queries.article.publish())`，修改成功后同一分组的查询自动失效，见 `.tenon/docs/frontend/client-react.md#tanstack`。
4. **列表**：查询条件、排序、分页放在地址中，用 `listSearch` 和 `useList`；表格见 `.tenon/docs/frontend/lists-forms.md#lists`、`.tenon/docs/frontend/lists-forms.md#data-table`。说明、备注这类长文字的列用 `TruncatedText`（一行显示，被截断时鼠标停在上面弹出全文），资源页面的文字列已经自动使用，见 `.tenon/docs/frontend/lists-forms.md#long-text`。
5. **表单**：用生成的校验规则和验证提示，与服务端一致，不手写规则，见 `.tenon/docs/frontend/lists-forms.md#forms`、`.tenon/docs/frontend/client-react.md#validation-rules`。
6. **按权限显示**：用生成的 `permissions.ts`，见 `.tenon/docs/frontend/client-react.md#ts-permissions`、`.tenon/docs/frontend/app.md#permissions`。
7. **检查**：`bun run typecheck`，然后在 `bun run dev` 打开的页面中看效果。

## 要点

- 业务规则、权限判断和校验写在 Go 中，前端只负责展示和交互。
- 增删改查和设置的后台页面可以在 Go 中声明，界面按声明渲染，见 `.tenon/docs/frontend/resources.md`。只对部分行有意义的操作（停用、启用）用 `rest.Action` 的 `When` 按行显示；密钥这类字段在 Go 中写 `format:"password"`，界面遮住中间部分；自己调用接口修改资源后用 `useResourceMutations` 的 `invalidate()` 刷新列表。声明表达不了的表单（嵌套字段、字段联动）只自己写表单的内容：`ResourceFormPage` 的 `children` 加上 `useResourceForm`，见 `.tenon/docs/frontend/resources.md#custom-forms`。
- 颜色从 Mantine 的主题中取：品牌色 `brand` 是默认色；其他彩色写预设的名称（`color="deep-orange"`、`var(--mantine-color-deep-orange-6)`），浅色底用 `-light` 变体；中性色用 `gray`、`dark` 色板、`c="dimmed"` 和 `--tn-*` 变量。不写十六进制或 `rgb()`，写死的值不随品牌色和暗色模式变化。
- 状态的颜色固定：成功 `green`，进行中 `bright-blue`，等待处理 `yellow`，需要注意 `deep-orange`，失败 `red`，没有状态 `gray`。Mantine 自带的 `teal`、`orange`、`blue`、`cyan`、`grape`、`indigo`、`lime` 不在主题的预设中，不使用。
- 不使用的库（antd、Tailwind 等）列在 `.tenon/docs/frontend/conventions.md#not-used`。
- 生成的代码（`packages/client`、`routeTree.gen.ts`）不手改。两个前端共用的业务组件放进 `packages/ui`。
- 服务端渲染的前端中，页面的 loader 在服务端运行时同样经生成的客户端调用接口，见 `.tenon/docs/frontend/start.md`。
