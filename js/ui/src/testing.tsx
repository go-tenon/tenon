// 测试用的应用：内存中的路由、已经登录的会话，外面套上 UIProvider。只在本包的测试中使用。

import { createSession } from "@go-tenon/client"
import { type App, AppRoot, createApp } from "@go-tenon/shell"
import { type AnyRoute, createMemoryHistory } from "@tanstack/react-router"
import { render } from "@testing-library/react"
import { UIProvider } from "./provider"
import { themes } from "./theme"

/** happy-dom 缺少的浏览器接口：Mantine 用到 matchMedia 和 ResizeObserver。 */
export function polyfill(): void {
  window.matchMedia ??= ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver
  // Mantine 的 ScrollArea 在测试中读取元素的尺寸。
  Element.prototype.scrollTo ??= () => {}
}

/** 渲染一个应用：signedIn 为 false 时未登录；permissions 是当前身份的权限。 */
export async function renderApp(
  routeTree: AnyRoute,
  options: { path?: string; signedIn?: boolean; permissions?: string[] } = {},
): Promise<App> {
  polyfill()
  const session = createSession({ load: async () => (options.signedIn === false ? null : { account: "u1" }) })
  const app = createApp({
    id: `ui-test-${Math.random()}`,
    routeTree,
    session,
    permissions: async () => options.permissions ?? [],
    router: { history: createMemoryHistory({ initialEntries: [options.path ?? "/"] }) },
  })
  render(
    <UIProvider theme={themes.console} defaultColorScheme="light">
      <AppRoot app={app} />
    </UIProvider>,
  )
  return app
}
