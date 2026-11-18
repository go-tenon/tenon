// 窗口状态的纯逻辑：读出保存的状态，判断保存的位置在当前的显示器上是否还看得到。

/** 窗口的位置和大小。 */
export interface Bounds {
  x: number
  y: number
  width: number
  height: number
}

/** 保存的窗口状态。 */
export interface WindowState {
  bounds: Bounds // 不最大化时的位置和大小
  maximized: boolean
}

/** 恢复窗口时接受的最小大小：更小的状态（例如在没有显示器的环境中保存的）不恢复。 */
const MIN_WIDTH = 200
const MIN_HEIGHT = 150

/** 解析保存的窗口状态；内容不完整或窗口小于 200×150 时返回 undefined。 */
export function parseState(text: string): WindowState | undefined {
  try {
    const s = JSON.parse(text) as Partial<WindowState>
    const b = s.bounds
    const ok =
      b &&
      [b.x, b.y, b.width, b.height].every((n) => Number.isFinite(n)) &&
      b.width >= MIN_WIDTH &&
      b.height >= MIN_HEIGHT
    return ok ? { bounds: b, maximized: s.maximized === true } : undefined
  } catch {
    return undefined
  }
}

/** 报告窗口 b 的标题栏（左上角 100×40 的区域）是否落在某个显示器的工作区 areas 中：拔掉显示器后窗口可能在屏幕之外。 */
export function visible(b: Bounds, areas: Bounds[]): boolean {
  const title = { x: b.x, y: b.y, width: Math.min(100, b.width), height: Math.min(40, b.height) }
  return areas.some(
    (a) =>
      title.x < a.x + a.width &&
      title.x + title.width > a.x &&
      title.y < a.y + a.height &&
      title.y + title.height > a.y,
  )
}
