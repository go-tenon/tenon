// AppLayout 告诉放在其中的组件的信息。

import { createContext, useContext } from "react"

const TabsAvailable = createContext(false)

/** AppLayout 开启了多标签时为 true：UserMenu 据此显示“多标签”的开关。 */
export const TabsAvailableProvider = TabsAvailable.Provider

/** 所在的 AppLayout 是否开启了多标签。 */
export function useTabsAvailable(): boolean {
  return useContext(TabsAvailable)
}
