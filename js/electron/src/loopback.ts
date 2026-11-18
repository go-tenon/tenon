// 登录回调：在 127.0.0.1 的随机端口上临时监听，接收浏览器跳回的 /callback 请求。

import { createServer, type ServerResponse } from "node:http"
import type { AddressInfo } from "node:net"

/** 一次登录的回调监听。 */
export interface Callback {
  redirectUri: string // http://127.0.0.1:<端口>/callback
  /** 等待浏览器跳回，返回回调的完整地址；超过 timeoutMs 或调用 close 时抛出错误。 */
  received(timeoutMs: number): Promise<string>
  /** 给浏览器返回一个页面，告诉用户登录的结果。 */
  respond(status: number, html: string): void
  /** 停止监听。 */
  close(): void
}

/** 在 127.0.0.1 的随机端口上开始监听登录回调。 */
export async function listenCallback(): Promise<Callback> {
  let resolve: (url: string) => void = () => {}
  let reject: (err: Error) => void = () => {}
  const arrived = new Promise<string>((res, rej) => {
    resolve = res
    reject = rej
  })
  arrived.catch(() => {}) // 没有人等待时不报未处理的拒绝
  let pending: ServerResponse | undefined
  const server = createServer((req, res) => {
    if (req.method !== "GET" || !req.url?.startsWith("/callback") || pending) {
      res.writeHead(404).end()
      return
    }
    pending = res
    resolve(`http://127.0.0.1:${port}${req.url}`)
  })
  await new Promise<void>((res, rej) => {
    server.once("error", rej)
    server.listen(0, "127.0.0.1", () => res())
  })
  const port = (server.address() as AddressInfo).port
  const close = () => {
    reject(new Error("sign-in was cancelled"))
    server.close()
    server.closeAllConnections()
  }
  return {
    redirectUri: `http://127.0.0.1:${port}/callback`,
    received(timeoutMs) {
      const timer = setTimeout(() => reject(new Error("sign-in timed out")), timeoutMs)
      return arrived.finally(() => clearTimeout(timer))
    },
    respond(status, html) {
      pending?.writeHead(status, { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store" }).end(html)
      pending = undefined
    },
    close,
  }
}
