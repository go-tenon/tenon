import { describe, expect, it } from "vitest"
import { breadcrumbs, buildMenu, firstPage, pageUnder, type RouteInfo } from "./menu"

// 路由树：根 → 受保护布局 _app（无标题）→ 首页、文章（分组）、设置（分组，只有需要权限的页面）、隐藏页。
const routes: RouteInfo[] = [
  { id: "__root__", path: "/" },
  { id: "/_app", path: "/", parent: "__root__" },
  { id: "/_app/", path: "/", parent: "/_app", staticData: { title: "首页", icon: "home", order: -1 } },
  { id: "/_app/articles", path: "/articles", parent: "/_app", staticData: { title: "文章", icon: "file-text" } },
  { id: "/_app/articles/", path: "/articles/", parent: "/_app/articles", staticData: { title: "全部文章", order: 2 } },
  {
    id: "/_app/articles/drafts",
    path: "/articles/drafts",
    parent: "/_app/articles",
    staticData: { title: "草稿", order: 1 },
  },
  { id: "/_app/articles/$id", path: "/articles/$id", parent: "/_app/articles", staticData: { title: "文章详情" } },
  { id: "/_app/settings", path: "/settings", parent: "/_app", staticData: { title: "设置", order: 9 } },
  {
    id: "/_app/settings/users",
    path: "/settings/users",
    parent: "/_app/settings",
    staticData: { title: "用户", permission: "users:manage" },
  },
  { id: "/_app/trash", path: "/trash", parent: "/_app", staticData: { title: "回收站", menu: false } },
  { id: "/login", path: "/login", parent: "__root__" },
]

describe("buildMenu", () => {
  it("按路由树嵌套、排序，去掉隐藏、带参数和没有权限的页面以及空分组", () => {
    expect(buildMenu(routes, () => false)).toEqual([
      { id: "/_app/", title: "首页", icon: "home", path: "/" },
      {
        id: "/_app/articles",
        title: "文章",
        icon: "file-text",
        path: "/articles",
        children: [
          { id: "/_app/articles/drafts", title: "草稿", path: "/articles/drafts" },
          { id: "/_app/articles/", title: "全部文章", path: "/articles" },
        ],
      },
    ])
    const all = buildMenu(routes, (p) => p === "users:manage")
    expect(all.map((i) => i.title)).toEqual(["首页", "文章", "设置"])
    expect(all[2].children?.map((i) => i.path)).toEqual(["/settings/users"])
  })

  it("firstPage 与 pageUnder", () => {
    const menu = buildMenu(routes, () => true)
    expect(firstPage(menu)).toBe("/")
    expect(firstPage(menu.slice(1))).toBe("/articles/drafts")
    expect(pageUnder(menu, "/articles/")).toBe("/articles/drafts")
    expect(pageUnder(menu, "/settings")).toBe("/settings/users")
    expect(pageUnder(menu, "/nowhere")).toBeUndefined()
    expect(firstPage([])).toBeUndefined()
  })
})

describe("breadcrumbs", () => {
  it("取带标题的路由，去掉相邻的重复", () => {
    expect(
      breadcrumbs([
        { pathname: "/" },
        { pathname: "/", staticData: {} },
        { pathname: "/articles", staticData: { title: "文章" } },
        { pathname: "/articles/", staticData: { title: "文章" } },
        { pathname: "/articles/42", staticData: { title: "文章详情" } },
      ]),
    ).toEqual([
      { title: "文章", path: "/articles" },
      { title: "文章详情", path: "/articles/42" },
    ])
  })
})
