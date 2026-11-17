import { createRouter } from "@tanstack/react-router"
import { cspNonce } from "../../src/index"
import { routeTree } from "./routeTree.gen"

/** 创建测试应用的路由，SSR 带上 CSP nonce。 */
export function getRouter() {
  return createRouter({ routeTree, ssr: { nonce: cspNonce() } })
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof getRouter>
  }
}
