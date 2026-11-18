// 布局交给放在其中的组件的内容。

import { createContext, type ReactNode, useContext } from "react"

const UserMenuExtra = createContext<ReactNode>(null)

/** 布局放进用户菜单的内容，例如 FrameLayout 开启多标签时的“多标签”开关；UserMenu 把它显示在配色的下面。 */
export const UserMenuExtraProvider = UserMenuExtra.Provider

/** 所在布局放进用户菜单的内容；没有时为 null。 */
export function useUserMenuExtra(): ReactNode {
  return useContext(UserMenuExtra)
}
