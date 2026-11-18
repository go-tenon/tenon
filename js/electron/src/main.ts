// @go-tenon/electron/main 在 Electron 的主进程中实现网页宿主的约定（@go-tenon/host）：
//
//   - 页面来自 https://<id>.localhost：用 protocol.handle 接管这个主机（其他 https 请求原样放行），前端按 SPA 模式
//     构建的文件从安装包返回，前端路由中的页面返回页面外壳（带 CSP 和 nonce）。用 https 而不是自定义协议：前端路由
//     只把 http(s) 的同源地址当作站内跳转，例如登录检查跳到登录页；.localhost 是保留的域名，不会解析到外部；
//   - 同源的 /api/* 转发到服务器，加上 Authorization: Bearer 和 X-App-Version，响应流式返回；收到 401 时删除 token；
//   - 实时频道的 WebSocket 握手加上 Authorization；
//   - 登录：在系统浏览器中走 OAuth 授权码 + PKCE，回调到 127.0.0.1，token 用 safeStorage 加密保存；
//   - 安全默认值：sandbox、contextIsolation、关闭 nodeIntegration，外链交给系统浏览器，权限请求默认拒绝。
//
// 其他桌面能力（单实例、托盘、通知等）用 Host.handle 注册方法、Host.emit 发出事件，由应用的 main.ts 组合。

import { randomBytes } from "node:crypto"
import { existsSync, readFileSync } from "node:fs"
import { join } from "node:path"
import { pathToFileURL } from "node:url"
import { createAuthorization, exchangeCode, type FetchLike, readCallback } from "@go-tenon/client"
import { HOST_CONTRACT, type HostEvents, type HostPlatform } from "@go-tenon/host"
import {
  app,
  BrowserWindow,
  type BrowserWindowConstructorOptions,
  type IpcMainInvokeEvent,
  ipcMain,
  net,
  protocol,
  safeStorage,
  session,
  shell,
} from "electron"
import { shellCSP, withNonce } from "./csp"
import { nullBodyStatus, requestHeaders, responseHeaders } from "./forward"
import { EVENT_PREFIX, type HostInit, INIT_ARG, METHOD_PREFIX } from "./ipc"
import { listenCallback } from "./loopback"
import { route } from "./routes"
import { TokenStore } from "./token"

/** 等待用户在浏览器中完成登录的最长时间，与授权码的有效期相同。 */
export const SIGN_IN_TIMEOUT = 10 * 60 * 1000

/** createHost 的选项。 */
export interface HostOptions {
  id: string // 客户端的名字（clients/<名>），小写字母、数字和 -：页面的主机名 <id>.localhost、OAuth 的 client ID、X-App-Version 的平台名
  server: string // 服务器的地址，例如 https://example.com：转发的目标，也是授权服务器的地址
  base: string // 前端的挂载路径，以 / 开头和结尾，例如 /admin/
  renderer: string // 前端按 SPA 模式构建的目录，其中有页面外壳 _shell.html
  preload: string // 构建好的 preload 脚本的路径
  proxy?: string[] // 转发到服务器的路径前缀，默认 ["/api/"]
  dev?: string // 开发时 Vite 开发服务器的地址，例如 http://localhost:5173；设置后页面和静态文件从它取，不检查 renderer
  window?: BrowserWindowConstructorOptions // 主窗口的其他选项，例如大小；webPreferences 中的安全选项由 createHost 决定
  signedInPage?: string // 登录成功后浏览器显示的 HTML
  failedPage?: string // 登录失败后浏览器显示的 HTML
}

/** createHost 返回的宿主。 */
export interface Host {
  readonly origin: string // 页面的来源，例如 https://admin-desktop.localhost
  /** 等应用就绪后注册协议、打开主窗口并返回它。只调用一次。 */
  start(): Promise<BrowserWindow>
  /** 主窗口；start 之前为 undefined。 */
  window(): BrowserWindow | undefined
  /**
   * 注册页面可以调用的方法 name，例如 notify、update.install，在 start 之前调用。fn 的参数和返回值都是 JSON。
   * 只接受来自本宿主页面的调用。
   */
  handle(name: string, fn: (...args: never[]) => unknown): void
  /** 向页面发出事件，例如通知被点击后的 navigate。 */
  emit<E extends keyof HostEvents>(event: E, data: HostEvents[E]): void
  /** 主窗口创建之后、加载页面之前调用 fn，例如恢复窗口的位置。在 start 之前调用。 */
  whenWindow(fn: (win: BrowserWindow) => void): void
  /** 每个转发到服务器的请求得到响应后调用 fn，例如服务端要求升级（426）时开始更新。 */
  onResponse(fn: (res: Response) => void): void
  /** 是否在开发模式（设置了 HostOptions.dev）。 */
  readonly dev: boolean
}

/** 宿主的种类。 */
const KIND = "electron"

/** 登录成功和失败时浏览器显示的默认页面。 */
const defaultSignedInPage =
  "<!doctype html><meta charset=utf-8><title>登录完成</title><p>登录完成，可以回到应用了。</p>"
const defaultFailedPage =
  "<!doctype html><meta charset=utf-8><title>登录失败</title><p>登录没有完成，请回到应用重试。</p>"

/** 创建宿主，之后调用 start 打开主窗口。选项不合法时抛出错误。 */
export function createHost(options: HostOptions): Host {
  if (!/^[a-z0-9][a-z0-9-]*$/.test(options.id)) throw new Error(`@go-tenon/electron: invalid id ${options.id}`)
  if (!options.base.startsWith("/") || !options.base.endsWith("/")) {
    throw new Error(`@go-tenon/electron: base ${options.base} must start and end with /`)
  }
  const server = new URL(options.server)
  if (server.protocol !== "https:" && server.protocol !== "http:") {
    throw new Error(`@go-tenon/electron: server ${options.server} must be an http(s) URL`)
  }
  const serverOrigin = server.origin
  const realtimeOrigin = serverOrigin.replace(/^http/, "ws")
  const issuer = options.server.replace(/\/$/, "")
  const hostname = `${options.id}.localhost`
  const origin = `https://${hostname}`
  const proxy = options.proxy ?? ["/api/"]
  const methods = new Map<string, (...args: never[]) => unknown>()
  const windowHooks: ((win: BrowserWindow) => void)[] = []
  const responseHooks: ((res: Response) => void)[] = []
  let win: BrowserWindow | undefined
  let tokens: TokenStore | undefined
  let started = false
  let signingIn: Promise<void> | undefined

  const appVersion = () => `${options.id}/${app.getVersion()}`

  /** 转发到服务器：带上 token 和版本，流式返回；收到 401 时删除发出请求时用的 token。 */
  const forward = async (req: Request, url: URL): Promise<Response> => {
    const token = await tokens?.get()
    const init: RequestInit & { duplex?: "half"; bypassCustomProtocolHandlers: boolean } = {
      method: req.method,
      headers: requestHeaders(req.headers, token, appVersion()),
      redirect: "manual",
      bypassCustomProtocolHandlers: true, // 服务器的 https 请求不再经过宿主自己的处理器
    }
    if (req.body) {
      init.body = req.body
      init.duplex = "half"
    }
    const res = await net.fetch(serverOrigin + url.pathname + url.search, init)
    if (res.status === 401 && token && tokens?.current() === token) tokens.clear()
    for (const fn of responseHooks) fn(res)
    return new Response(nullBodyStatus(res.status) ? null : res.body, {
      status: res.status,
      statusText: res.statusText,
      headers: responseHeaders(res.headers),
    })
  }

  /** 处理 https 请求：页面的主机由宿主返回，其他主机原样放行。 */
  const serve = async (req: Request): Promise<Response> => {
    const url = new URL(req.url)
    if (url.host !== hostname) return net.fetch(req, { bypassCustomProtocolHandlers: true })
    const target = route(url.pathname, { root: options.renderer, base: options.base, proxy })
    if (target.kind === "proxy") return forward(req, url)
    if (options.dev) {
      // 开发时页面、模块和 HMR 客户端都由 Vite 返回；只转发 Accept，Origin 原样交给 net.fetch 会失败。
      return net.fetch(options.dev.replace(/\/$/, "") + url.pathname + url.search, {
        method: req.method,
        headers: { Accept: req.headers.get("Accept") ?? "*/*" },
        bypassCustomProtocolHandlers: true,
      })
    }
    switch (target.kind) {
      case "file":
        return net.fetch(pathToFileURL(target.path).toString())
      case "shell": {
        const nonce = randomBytes(16).toString("base64")
        return new Response(withNonce(shellHTML(), nonce), {
          headers: {
            "Content-Type": "text/html; charset=utf-8",
            "Content-Security-Policy": shellCSP(nonce, [realtimeOrigin]),
          },
        })
      }
      default:
        return new Response(null, { status: 404 })
    }
  }

  let shell_: string | undefined
  const shellHTML = () => {
    shell_ ??= readFileSync(join(options.renderer, "_shell.html"), "utf8")
    return shell_
  }

  /** 在系统浏览器中登录；同时只进行一次，重复调用得到同一个结果。 */
  const signIn = (): Promise<void> => {
    signingIn ??= (async () => {
      const callback = await listenCallback()
      try {
        const auth = await createAuthorization({ issuer, clientId: options.id, redirectUri: callback.redirectUri })
        await openExternal(auth.url)
        const received = await callback.received(SIGN_IN_TIMEOUT)
        let code: string
        try {
          code = readCallback(received, auth, issuer)
        } catch (err) {
          callback.respond(400, options.failedPage ?? defaultFailedPage)
          throw err
        }
        callback.respond(200, options.signedInPage ?? defaultSignedInPage)
        const fetch = ((url: string, init?: RequestInit) =>
          net.fetch(url, { ...init, bypassCustomProtocolHandlers: true })) as FetchLike
        const token = await exchangeCode({ issuer, clientId: options.id, auth, code, fetch })
        await tokens?.set(token)
        if (win?.isMinimized()) win.restore()
        win?.focus()
      } finally {
        callback.close()
      }
    })().finally(() => {
      signingIn = undefined
    })
    return signingIn
  }

  const host: Host = {
    origin,
    dev: options.dev !== undefined,
    whenWindow(fn) {
      if (started) throw new Error("@go-tenon/electron: call whenWindow before start")
      windowHooks.push(fn)
    },
    onResponse(fn) {
      responseHooks.push(fn)
    },
    window: () => win,
    handle(name, fn) {
      if (started) throw new Error(`@go-tenon/electron: register ${name} before start`)
      if (methods.has(name)) throw new Error(`@go-tenon/electron: ${name} is already registered`)
      methods.set(name, fn)
    },
    emit(event, data) {
      win?.webContents.send(EVENT_PREFIX + event, data)
    },
    async start() {
      if (started) throw new Error("@go-tenon/electron: start was already called")
      started = true
      if (!options.dev && !existsSync(join(options.renderer, "_shell.html"))) {
        throw new Error(`@go-tenon/electron: no _shell.html in ${options.renderer}; build the frontend first`)
      }
      if (!existsSync(options.preload)) throw new Error(`@go-tenon/electron: preload ${options.preload} does not exist`)
      await app.whenReady()

      // Linux 上没有钥匙串服务（gnome-keyring、KWallet）时，与 Chromium 一样退回固定的密钥。
      if (process.platform === "linux" && !(await safeStorage.isAsyncEncryptionAvailable())) {
        safeStorage.setUsePlainTextEncryption(true)
        console.warn("@go-tenon/electron: no system keyring; the token is encrypted with a fixed key, as Chromium does")
      }
      tokens = new TokenStore(
        join(app.getPath("userData"), "token"),
        {
          encrypt: (s) => safeStorage.encryptStringAsync(s),
          decrypt: async (b) => (await safeStorage.decryptStringAsync(Buffer.from(b))).result,
        },
        (err) => console.warn("@go-tenon/electron: the saved token could not be decrypted; sign in again", err),
      )
      protocol.handle("https", serve)
      session.defaultSession.webRequest.onBeforeSendHeaders({ urls: [`${realtimeOrigin}/*`] }, (details, done) => {
        if (details.resourceType !== "webSocket") return done({ requestHeaders: details.requestHeaders })
        void tokens?.get().then((token) =>
          done({
            requestHeaders: token
              ? { ...details.requestHeaders, Authorization: `Bearer ${token}` }
              : details.requestHeaders,
          }),
        )
      })
      session.defaultSession.setPermissionRequestHandler((_contents, permission, allow) => {
        allow(permission === "clipboard-sanitized-write" || permission === "fullscreen")
      })
      app.on("web-contents-created", (_e, contents) => {
        contents.on("will-attach-webview", (e) => e.preventDefault())
        contents.on("will-navigate", (e, url) => {
          if (url.startsWith(`${origin}/`)) return
          e.preventDefault()
          void openExternal(url)
        })
        contents.setWindowOpenHandler(({ url }) => {
          void openExternal(url)
          return { action: "deny" }
        })
      })

      methods.set("auth.signIn", signIn)
      methods.set("auth.signOut", async () => tokens?.clear())
      for (const [name, fn] of methods) {
        const call = fn as (...args: unknown[]) => unknown
        ipcMain.handle(METHOD_PREFIX + name, (event: IpcMainInvokeEvent, ...args: unknown[]) => {
          if (!event.senderFrame?.url.startsWith(`${origin}/`))
            throw new Error(`${name}: caller is not this host's page`)
          return call(...args)
        })
      }

      const init: HostInit = {
        contract: HOST_CONTRACT,
        kind: KIND,
        platform: platform(),
        appVersion: app.getVersion(),
        methods: [...methods.keys()],
      }
      win = new BrowserWindow({
        width: 1280,
        height: 800,
        show: false,
        ...options.window,
        webPreferences: {
          ...options.window?.webPreferences,
          preload: options.preload,
          sandbox: true,
          contextIsolation: true,
          nodeIntegration: false,
          webSecurity: true,
          additionalArguments: [INIT_ARG + JSON.stringify(init)],
        },
      })
      const shown = win
      for (const fn of windowHooks) fn(shown)
      shown.once("ready-to-show", () => shown.show())
      if (options.dev) await waitForServer(options.dev, DEV_SERVER_TIMEOUT)
      await shown.loadURL(`${origin}${options.base}`)
      return shown
    },
  }
  return host
}

/** 开发时等待前端开发服务器启动的最长时间：tenon dev 同时启动它和 Electron。 */
const DEV_SERVER_TIMEOUT = 60 * 1000

/** 等到 url 能够访问；超过 timeoutMs 时抛出错误。 */
async function waitForServer(url: string, timeoutMs: number): Promise<void> {
  const deadline = Date.now() + timeoutMs
  for (;;) {
    try {
      await net.fetch(url, { method: "HEAD" })
      return
    } catch (err) {
      if (Date.now() > deadline)
        throw new Error(`@go-tenon/electron: dev server ${url} is not reachable`, { cause: err })
      await new Promise((resolve) => setTimeout(resolve, 300))
    }
  }
}

/** 在系统浏览器或邮件程序中打开 url；其他协议不打开。 */
function openExternal(url: string): Promise<void> {
  if (!/^(https?|mailto):/i.test(url)) return Promise.resolve()
  return shell.openExternal(url)
}

/** 返回约定中的系统名称。 */
function platform(): HostPlatform {
  switch (process.platform) {
    case "darwin":
      return "macos"
    case "win32":
      return "windows"
    default:
      return "linux"
  }
}

export {
  autostart,
  badge,
  type DeepLinkOptions,
  deepLinks,
  notifications,
  type TrayOptions,
  tray,
} from "./integrations"
export { type LogOptions, logs } from "./logs"
export { standardMenu } from "./menu"
export { singleInstance } from "./single-instance"
export { windowState } from "./window-state"
