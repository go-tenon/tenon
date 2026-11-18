// @go-tenon/host 是网页宿主的约定在页面一侧的类型：apps 中的前端运行在 Electron、Tauri、Wails 这类宿主的窗口中时，
// 宿主在全局放一个 tenonHost，页面通过 host() 取得它，调用登录、系统通知、角标等桌面能力。
//
// 宿主同时负责页面看不到的部分：从本地来源加载前端，把同源的 /api/* 转发到服务器并加上 Authorization: Bearer，
// token 只在宿主中，收到 401 时删除 token。所以页面中的接口调用在网页和各个宿主中完全相同。
//
// 约定要能用任何语言实现宿主：每个方法都是异步的，参数和返回值都是 JSON；可选的能力由宿主决定有没有，
// 页面先判断成员是否存在再调用；contract 是约定的版本。这个包不依赖任何包和 DOM。

/** 约定的版本。宿主的 tenonHost.contract 等于它时，下面的成员按这里的说明工作。 */
export const HOST_CONTRACT = 1

/** 宿主所在的系统。 */
export type HostPlatform = "windows" | "macos" | "linux" | "ios" | "android"

/** 更新的状态。 */
export interface UpdateStatus {
  state: "none" | "available" | "downloading" | "ready" // 没有更新、有新版本、正在下载、下载完成等待安装
  version?: string // 新版本的版本号，state 为 none 时没有
}

/** 系统通知。 */
export interface HostNotification {
  title: string
  body?: string
  path?: string // 用户点击通知时页面要跳转到的路径，例如 /admin/orders/123，经 navigate 事件交给页面
}

/** 宿主发给页面的事件及其数据。 */
export interface HostEvents {
  navigate: string // 页面要跳转到的路径：点击了通知，或者从深度链接打开
  update: UpdateStatus // 更新的状态变了
}

/** 宿主放在全局的 tenonHost。可选的成员是宿主可以不提供的能力。 */
export interface TenonHost {
  readonly contract: number // 约定的版本，见 HOST_CONTRACT
  readonly kind: string // 宿主的种类，例如 electron
  readonly platform: HostPlatform
  readonly appVersion: string // 客户端的版本，与 X-App-Version 中的相同

  /** 登录和退出。token 只在宿主中，页面只知道登录是否完成。 */
  readonly auth: {
    /** 在系统浏览器中登录，token 保存好之后完成；用户取消或超时时抛出错误。之后页面照常读取当前用户。 */
    signIn(): Promise<void>
    /** 删除宿主保存的 token。先调用应用的退出接口撤销这次登录，再调用它。 */
    signOut(): Promise<void>
  }

  /** 显示系统通知。 */
  notify?(notification: HostNotification): Promise<void>
  /** 设置未读角标，0 表示清除。 */
  badge?(count: number): Promise<void>
  /** 检查和安装更新。服务端返回 426 app.upgrade_required 时调用 install。 */
  readonly update?: {
    check(): Promise<UpdateStatus>
    /** 安装已经下载的更新并重启；还没有下载时先下载。 */
    install(): Promise<void>
  }
  /** 开机自启。 */
  readonly autostart?: {
    get(): Promise<boolean>
    set(enabled: boolean): Promise<void>
  }

  /** 订阅宿主的事件，返回取消订阅的函数。 */
  on<E extends keyof HostEvents>(event: E, listener: (data: HostEvents[E]) => void): () => void
}

/** 返回宿主放在全局的 tenonHost；页面在浏览器中运行时返回 undefined。 */
export function host(): TenonHost | undefined {
  return (globalThis as { tenonHost?: TenonHost }).tenonHost
}
