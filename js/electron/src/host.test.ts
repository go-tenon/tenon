import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { describe, expect, it, vi } from "vitest"
import { PRERENDER_NONCE, shellCSP, withNonce } from "./csp"
import { nullBodyStatus, requestHeaders, responseHeaders } from "./forward"
import { listenCallback } from "./loopback"
import { route } from "./routes"
import { TokenStore } from "./token"

describe("route", () => {
  const root = mkdtempSync(join(tmpdir(), "tenon-electron-"))
  mkdirSync(join(root, "assets"))
  writeFileSync(join(root, "assets", "app.js"), "")
  writeFileSync(join(root, "_shell.html"), "")
  writeFileSync(join(tmpdir(), "secret.txt"), "")
  const opts = { root, base: "/admin/", proxy: ["/api/"] }

  it("接口转发到服务器，文件从安装包返回，前端路由中的页面返回外壳", () => {
    expect(route("/api/me", opts)).toEqual({ kind: "proxy" })
    expect(route("/admin/assets/app.js", opts)).toEqual({ kind: "file", path: join(root, "assets", "app.js") })
    expect(route("/admin/", opts)).toEqual({ kind: "shell" })
    expect(route("/admin", opts)).toEqual({ kind: "shell" })
    expect(route("/admin/orders/123", opts)).toEqual({ kind: "shell" })
  })

  it("base 以外的路径、找不到的文件、越出前端目录的路径不存在", () => {
    expect(route("/other/", opts)).toEqual({ kind: "missing" })
    expect(route("/admin/assets/missing.js", opts)).toEqual({ kind: "missing" })
    expect(route("/admin/../secret.txt", opts)).toEqual({ kind: "missing" })
    expect(route("/admin/%2e%2e/secret.txt", opts)).toEqual({ kind: "missing" })
  })
})

describe("转发的请求头和响应头", () => {
  it("去掉页面的来源、cookie 和自己设置的认证，加上宿主的 token 和版本", () => {
    const h = requestHeaders(
      new Headers({
        Origin: "https://admin.localhost",
        Referer: "https://admin.localhost/admin/",
        Cookie: "a=1",
        Authorization: "Bearer from-page",
        "X-App-Version": "fake/9",
        "Content-Type": "application/json",
        "Last-Event-ID": "42",
      }),
      "tok",
      "admin-desktop/1.2.0",
    )
    expect(Object.fromEntries(h)).toEqual({
      authorization: "Bearer tok",
      "content-type": "application/json",
      "last-event-id": "42",
      "x-app-version": "admin-desktop/1.2.0",
    })
    expect(requestHeaders(new Headers(), undefined, "v/1").has("Authorization")).toBe(false)
  })

  it("响应去掉 Set-Cookie", () => {
    const h = responseHeaders(new Headers({ "Set-Cookie": "__Host-session=x", "Content-Type": "text/event-stream" }))
    expect(Object.fromEntries(h)).toEqual({ "content-type": "text/event-stream" })
    expect([204, 304].map(nullBodyStatus)).toEqual([true, true])
    expect(nullBodyStatus(200)).toBe(false)
  })
})

describe("页面外壳的 CSP", () => {
  it("内联脚本的占位 nonce 换成本次的，允许连接实时频道", () => {
    expect(withNonce(`<script nonce="${PRERENDER_NONCE}"></script>`, "n1")).toBe('<script nonce="n1"></script>')
    const csp = shellCSP("n1", ["wss://example.com"])
    expect(csp).toContain("script-src 'self' 'nonce-n1'")
    expect(csp).toContain("connect-src 'self' wss://example.com")
    expect(csp).toContain("frame-ancestors 'none'")
  })
})

describe("TokenStore", () => {
  const cipher = {
    encrypt: async (s: string) => new TextEncoder().encode(`enc:${s}`),
    decrypt: async (b: Uint8Array) => {
      const s = new TextDecoder().decode(b)
      if (!s.startsWith("enc:")) throw new Error("bad key")
      return s.slice(4)
    },
  }

  it("加密保存，重新打开后读出；clear 删除文件", async () => {
    const file = join(mkdtempSync(join(tmpdir(), "tenon-token-")), "dir", "token")
    await new TokenStore(file, cipher).set("tok")
    expect(readFileSync(file, "utf8")).toBe("enc:tok")
    const store = new TokenStore(file, cipher)
    expect(store.current()).toBeUndefined()
    expect(await store.get()).toBe("tok")
    expect(store.current()).toBe("tok")
    store.clear()
    expect(existsSync(file)).toBe(false)
    expect(await new TokenStore(file, cipher).get()).toBeUndefined()
  })

  it("无法解密时删除文件，当作没有登录", async () => {
    const file = join(mkdtempSync(join(tmpdir(), "tenon-token-")), "token")
    writeFileSync(file, "garbage")
    const unreadable = vi.fn()
    expect(await new TokenStore(file, cipher, unreadable).get()).toBeUndefined()
    expect(unreadable).toHaveBeenCalledOnce()
    expect(existsSync(file)).toBe(false)
  })
})

describe("listenCallback", () => {
  it("收到 /callback 后交出完整地址，并给浏览器返回页面", async () => {
    const cb = await listenCallback()
    expect(cb.redirectUri).toMatch(/^http:\/\/127\.0\.0\.1:\d+\/callback$/)
    expect((await fetch(cb.redirectUri.replace("/callback", "/other"))).status).toBe(404)
    const browser = fetch(`${cb.redirectUri}?code=c1&state=s1`)
    expect(await cb.received(5000)).toBe(`${cb.redirectUri}?code=c1&state=s1`)
    cb.respond(200, "<p>ok</p>")
    const res = await browser
    expect(res.status).toBe(200)
    expect(await res.text()).toBe("<p>ok</p>")
    cb.close()
  })

  it("超时或关闭时抛出错误", async () => {
    const cb = await listenCallback()
    await expect(cb.received(10)).rejects.toThrow("timed out")
    cb.close()
    const closed = await listenCallback()
    const waiting = closed.received(5000)
    closed.close()
    await expect(waiting).rejects.toThrow("cancelled")
  })
})
