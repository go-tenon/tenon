// 按应用隔离的本地存储：同一个域名下的多个前端（例如 web 和 admin）各用自己的命名空间。

/** 浏览器 localStorage 中需要的部分。 */
export interface StorageLike {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

/** 应用的本地存储，值以 JSON 保存。存储不可用（隐私模式、被禁用）时读到 undefined，写入被忽略。 */
export interface AppStorage {
  /** 完整的键：tenon:<应用>:<name>。 */
  key(name: string): string
  get<T>(name: string): T | undefined
  set(name: string, value: unknown): void
  remove(name: string): void
}

/** 创建应用 id 的存储；backend 默认是 localStorage。 */
export function appStorage(id: string, backend?: StorageLike): AppStorage {
  const store = (): StorageLike | undefined => {
    try {
      return backend ?? globalThis.localStorage
    } catch {
      return undefined
    }
  }
  const key = (name: string) => `tenon:${id}:${name}`
  return {
    key,
    get<T>(name: string) {
      try {
        const raw = store()?.getItem(key(name))
        return raw == null ? undefined : (JSON.parse(raw) as T)
      } catch {
        return undefined
      }
    },
    set(name, value) {
      try {
        store()?.setItem(key(name), JSON.stringify(value))
      } catch {
        // 存储已满或不可用：偏好只在本次会话中有效。
      }
    },
    remove(name) {
      try {
        store()?.removeItem(key(name))
      } catch {
        // 同上。
      }
    },
  }
}
