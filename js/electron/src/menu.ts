// 应用菜单和右键菜单。macOS 上没有“编辑”菜单时 ⌘C、⌘V 等快捷键不起作用，所以给出标准菜单；Windows 和 Linux 上
// 的窗口没有菜单栏，快捷键由页面处理。开发时多一个“视图”菜单，可以重新加载和打开开发者工具。

import { app, clipboard, Menu, type MenuItemConstructorOptions } from "electron"
import { contextTemplate } from "./context-menu"
import type { Host } from "./main"

/** 设置应用菜单和页面的右键菜单。 */
export function standardMenu(host: Host): void {
  const view: MenuItemConstructorOptions = {
    label: "视图",
    submenu: [
      ...(host.dev
        ? ([
            { role: "reload", label: "重新加载" },
            { role: "toggleDevTools", label: "开发者工具" },
            { type: "separator" },
          ] satisfies MenuItemConstructorOptions[])
        : []),
      { role: "resetZoom", label: "实际大小" },
      { role: "zoomIn", label: "放大" },
      { role: "zoomOut", label: "缩小" },
      { type: "separator" },
      { role: "togglefullscreen", label: "全屏" },
    ],
  }
  const template: MenuItemConstructorOptions[] =
    process.platform === "darwin"
      ? [
          { role: "appMenu" },
          {
            label: "编辑",
            submenu: [
              { role: "undo", label: "撤销" },
              { role: "redo", label: "重做" },
              { type: "separator" },
              { role: "cut", label: "剪切" },
              { role: "copy", label: "复制" },
              { role: "paste", label: "粘贴" },
              { role: "selectAll", label: "全选" },
            ],
          },
          view,
          { role: "windowMenu", label: "窗口" },
        ]
      : host.dev
        ? [view]
        : []
  app.whenReady().then(() => Menu.setApplicationMenu(template.length > 0 ? Menu.buildFromTemplate(template) : null))

  host.whenWindow((win) => {
    win.webContents.on("context-menu", (_e, params) => {
      const items = contextTemplate(params, win.webContents, (url) => clipboard.writeText(url))
      if (items.length > 0) Menu.buildFromTemplate(items).popup({ window: win })
    })
  })
}
