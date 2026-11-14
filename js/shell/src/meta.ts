// 路由的元数据：标题、图标、排序、权限，写在路由的 staticData 中，用来生成菜单、面包屑和检查权限。

declare module "@tanstack/react-router" {
  interface StaticDataRouteOption {
    /** 菜单、面包屑和标签中显示的标题。没有标题的路由不出现在菜单和面包屑中。 */
    title?: string
    /** 图标的名字，例如 "file-text"。由外观层映射为组件库的图标，shell 不依赖图标库。 */
    icon?: string
    /** 在同级菜单中的顺序，小的在前；相同时按路径排序。 */
    order?: number
    /** 进入页面需要的权限，通常是生成的 permissions.ts 中的 Permission。带权限的页面同时要求已经登录。 */
    permission?: string
    /** false 时不出现在菜单中，例如详情页；带路径参数的页面总是不出现。 */
    menu?: boolean
  }
}

export {}
