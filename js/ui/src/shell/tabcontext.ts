// 标签页面的上下文：页面在哪个标签中、是否是当前标签，以及登记未保存修改的函数。
// 单独一个文件：表单（useUnsavedChanges）和多标签都用它，彼此不互相导入。

import { createContext } from "react"

/** 页面所在的标签。 */
export interface TabPane {
  key: string
  active: boolean
  /** 登记这个标签有没有未保存的修改。 */
  setDirty(dirty: boolean): void
}

/** 当前页面所在标签的上下文，不在标签中时为 null。 */
export const TabPaneContext = createContext<TabPane | null>(null)
