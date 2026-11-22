// 路由的元数据：标题、图标、排序、权限和版面，写在路由的 staticData 中，用来生成菜单、面包屑、检查权限和决定版面。

declare module "@tanstack/react-router" {
  interface StaticDataRouteOption {
    /** 菜单、面包屑和标签中显示的标题。没有标题的路由不出现在菜单和面包屑中。 */
    title?: string
    /** 图标的名字，例如 "file-text"。由 IconsProvider 映射为图标组件。 */
    icon?: string
    /** 在同级菜单中的顺序，小的在前；相同时按路径排序。 */
    order?: number
    /** 进入页面需要的权限，通常是生成的 permissions.ts 中的 Permission。带权限的页面同时要求已经登录。 */
    permission?: string
    /** false 时不出现在菜单中，例如详情页；带路径参数的页面总是不出现。 */
    menu?: boolean
    /**
     * true 时页面铺满布局的内容区：没有最大宽度、内边距和面包屑，高度占满，内容区本身不滚动，由页面中的各栏自己滚动。
     * 写在模块的路由上时，模块下的页面都铺满，例如收件箱、聊天。
     */
    fullBleed?: boolean
    /**
     * 在模块二级导航中切换页面时自动保留的 URL 查询参数名，例如 ["preset", "from", "to", "team_id"]。
     * 写在模块的父路由上时，对其下全部子页面生效。
     */
    preserveSearch?: readonly string[]
  }
}

export {}
