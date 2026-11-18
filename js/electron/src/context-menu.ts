// 右键菜单的内容：输入框中是剪切、复制、粘贴、全选和拼写建议，选中的文字可以复制，链接可以复制地址。

import type { ContextMenuParams, MenuItemConstructorOptions, WebContents } from "electron"

/** 右键菜单中用到的 ContextMenuParams 字段。 */
export type MenuParams = Pick<
  ContextMenuParams,
  "isEditable" | "selectionText" | "linkURL" | "misspelledWord" | "dictionarySuggestions" | "editFlags"
>

/** 返回右键菜单的菜单项；没有可以做的事时返回空数组，不弹出菜单。copyLink 把链接地址写入剪贴板。 */
export function contextTemplate(
  params: MenuParams,
  contents: Pick<WebContents, "replaceMisspelling">,
  copyLink: (url: string) => void,
): MenuItemConstructorOptions[] {
  const items: MenuItemConstructorOptions[] = []
  if (params.misspelledWord) {
    for (const word of params.dictionarySuggestions.slice(0, 5)) {
      items.push({ label: word, click: () => contents.replaceMisspelling(word) })
    }
    if (items.length > 0) items.push({ type: "separator" })
  }
  if (params.isEditable) {
    const f = params.editFlags
    items.push(
      { role: "cut", label: "剪切", enabled: f.canCut },
      { role: "copy", label: "复制", enabled: f.canCopy },
      { role: "paste", label: "粘贴", enabled: f.canPaste },
      { type: "separator" },
      { role: "selectAll", label: "全选", enabled: f.canSelectAll },
    )
  } else if (params.selectionText.trim() !== "") {
    items.push({ role: "copy", label: "复制" })
  }
  if (params.linkURL) {
    if (items.length > 0) items.push({ type: "separator" })
    items.push({ label: "复制链接地址", click: () => copyLink(params.linkURL) })
  }
  return items
}
