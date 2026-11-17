// 一个可以订阅的小型状态容器，供插槽、徽标和布局状态使用：React 用 useSyncExternalStore 读取。

/** 可以订阅的状态容器。 */
export interface Store<T> {
  get(): T
  set(next: T): void
  subscribe(listener: () => void): () => void
}

/** 创建初始值为 initial 的状态容器。 */
export function createStore<T>(initial: T): Store<T> {
  let value = initial
  const listeners = new Set<() => void>()
  return {
    get: () => value,
    set(next) {
      if (Object.is(next, value)) return
      value = next
      for (const l of [...listeners]) l()
    },
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
  }
}

/** 每个应用（或其他对象）一份的状态，例如一个应用的插槽内容。 */
export function perOwner<T>(make: () => T): (owner: object) => T {
  const all = new WeakMap<object, T>()
  return (owner) => {
    let v = all.get(owner)
    if (v === undefined) {
      v = make()
      all.set(owner, v)
    }
    return v
  }
}
