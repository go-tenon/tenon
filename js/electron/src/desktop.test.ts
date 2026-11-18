import { existsSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { describe, expect, it, vi } from "vitest"
import { parseState, visible } from "./bounds"
import { contextTemplate, type MenuParams } from "./context-menu"
import { appendLog } from "./logfile"

describe("窗口状态", () => {
  it("读出保存的位置和是否最大化，内容不完整或窗口太小时忽略", () => {
    const bounds = { x: 10, y: 20, width: 800, height: 600 }
    expect(parseState(JSON.stringify({ bounds, maximized: true }))).toEqual({ bounds, maximized: true })
    expect(parseState(JSON.stringify({ bounds }))).toEqual({ bounds, maximized: false })
    expect(parseState(JSON.stringify({ bounds: { x: 1, y: 2, width: 0, height: 600 } }))).toBeUndefined()
    expect(parseState(JSON.stringify({ bounds: { x: 0, y: 0, width: 1, height: 1 } }))).toBeUndefined()
    expect(parseState(JSON.stringify({ maximized: true }))).toBeUndefined()
    expect(parseState("{")).toBeUndefined()
  })

  it("标题栏落在某个显示器上才算看得到", () => {
    const screens = [
      { x: 0, y: 0, width: 1920, height: 1040 },
      { x: 1920, y: 0, width: 1280, height: 1000 },
    ]
    expect(visible({ x: 100, y: 100, width: 800, height: 600 }, screens)).toBe(true)
    expect(visible({ x: 2000, y: 50, width: 800, height: 600 }, screens)).toBe(true) // 第二个显示器
    expect(visible({ x: 3500, y: 50, width: 800, height: 600 }, screens)).toBe(false) // 拔掉的显示器
    expect(visible({ x: 100, y: -500, width: 800, height: 600 }, screens)).toBe(false) // 标题栏在屏幕上方
  })
})

describe("右键菜单", () => {
  const params = (p: Partial<MenuParams>): MenuParams => ({
    isEditable: false,
    selectionText: "",
    linkURL: "",
    misspelledWord: "",
    dictionarySuggestions: [],
    editFlags: {
      canUndo: false,
      canRedo: false,
      canCut: true,
      canCopy: true,
      canPaste: true,
      canDelete: true,
      canSelectAll: true,
      canEditRichly: false,
    },
    ...p,
  })
  const contents = { replaceMisspelling: vi.fn() }
  const labels = (p: Partial<MenuParams>) =>
    contextTemplate(params(p), contents, () => {}).map((i) => i.label ?? i.type)

  it("输入框中有编辑操作，拼写错误时先给出建议", () => {
    expect(labels({ isEditable: true })).toEqual(["剪切", "复制", "粘贴", "separator", "全选"])
    const items = contextTemplate(
      params({ isEditable: true, misspelledWord: "helo", dictionarySuggestions: ["hello", "help"] }),
      contents,
      () => {},
    )
    expect(items.slice(0, 3).map((i) => i.label ?? i.type)).toEqual(["hello", "help", "separator"])
    items[0].click?.({} as never, undefined, {} as never)
    expect(contents.replaceMisspelling).toHaveBeenCalledWith("hello")
  })

  it("选中的文字可以复制，链接可以复制地址，空白处没有菜单", () => {
    expect(labels({ selectionText: "abc" })).toEqual(["复制"])
    const copied: string[] = []
    const items = contextTemplate(params({ linkURL: "https://example.com/a" }), contents, (u) => copied.push(u))
    expect(items.map((i) => i.label)).toEqual(["复制链接地址"])
    items[0].click?.({} as never, undefined, {} as never)
    expect(copied).toEqual(["https://example.com/a"])
    expect(labels({})).toEqual([])
  })
})

describe("appendLog", () => {
  it("超过大小时换成 .old，只保留一份旧文件", () => {
    const file = join(mkdtempSync(join(tmpdir(), "tenon-log-")), "logs", "main.log")
    appendLog(file, "a".repeat(20), 30)
    appendLog(file, "b".repeat(20), 30)
    expect(existsSync(`${file}.old`)).toBe(false)
    appendLog(file, "c", 30) // 已有 42 字节，先换走
    expect(readFileSync(`${file}.old`, "utf8")).toBe(`${"a".repeat(20)}\n${"b".repeat(20)}\n`)
    expect(readFileSync(file, "utf8")).toBe("c\n")
    writeFileSync(file, "x".repeat(40))
    appendLog(file, "d", 30)
    expect(readFileSync(`${file}.old`, "utf8")).toBe("x".repeat(40))
  })
})
