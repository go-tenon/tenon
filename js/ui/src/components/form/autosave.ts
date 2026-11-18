// 自动保存：表单的值变化后，等用户停一会儿再保存。与表单库无关：表单把值的变化交给它，SettingsForm 中是 @mantine/form。
//
// 规则：
//   - 值变化后等 delay（默认 600 毫秒）再保存，期间再变化就重新计时；
//   - 校验不通过时不保存，等用户改正（字段自己的校验会提示）；
//   - 与上次保存成功的值相同时不保存；
//   - 保存一次只进行一个：保存期间又有变化，本次完成后再保存一次最新的值；
//   - save 返回 false 或抛出错误表示没有保存成功，下次变化时重新提交；
//   - 停止（例如离开页面）时，还在等待的变化立即保存，除非 discard 放弃了它们；resume 恢复接收变化。
//
// 在 React 中，effect 激活时 resume、清理时 stop：StrictMode 会先清理再重新激活，Activity 隐藏时清理、显示时
// 重新激活，同一个实例要能停下再继续。

/** 自动保存的状态，外观据此显示“保存中”“已保存”“保存失败”。 */
export type AutoSaveStatus = "idle" | "pending" | "saving" | "saved" | "error"

/** createAutoSaver 的选项。 */
export interface AutoSaverOptions<T> {
  /** 保存：调用接口。返回 false 或抛出错误表示没有保存成功。 */
  save: (values: T) => Promise<unknown>
  /** 校验，返回 false 时不保存。默认都可以保存。 */
  validate?: (values: T) => boolean
  /** 停下多久后保存（毫秒），默认 600。 */
  delay?: number
  /** 保存抛出的错误。 */
  onError?: (err: unknown) => void
}

/** 自动保存。 */
export interface AutoSaver<T> {
  /** 值变化了：等 delay 后保存。 */
  change(values: T): void
  /** 立即保存最新的值（不等 delay）。force 为 true 时即使与上次保存的相同也提交，例如表单之外的改动。 */
  flush(force?: boolean): Promise<void>
  /** 把 values 当作已经保存的值，例如服务端返回了新的值、表单重新加载了数据。 */
  markSaved(values: T): void
  /** 放弃还在等待的变化，之后的 stop 不再保存它们。 */
  discard(): void
  /** 停止：还在等待的变化立即保存（discard 之后除外）。之后的 change 不再生效，直到 resume。 */
  stop(): Promise<void>
  /** 停止之后恢复接收变化，例如组件重新显示。没有停止时什么也不做。 */
  resume(): void
  status(): AutoSaveStatus
  subscribe(listener: () => void): () => void
}

/** 创建自动保存。initial 是当前已经保存的值（通常是表单的初始值）。 */
export function createAutoSaver<T>(initial: T, options: AutoSaverOptions<T>): AutoSaver<T> {
  const delay = options.delay ?? 600
  const listeners = new Set<() => void>()
  let saved = JSON.stringify(initial)
  let latest: T = initial
  let timer: ReturnType<typeof setTimeout> | undefined
  let saving: Promise<void> | undefined
  let queued: "changed" | "forced" | undefined
  let stopped = false
  let current: AutoSaveStatus = "idle"

  const setStatus = (next: AutoSaveStatus) => {
    if (next === current) return
    current = next
    for (const l of listeners) l()
  }
  const clearTimer = () => {
    if (timer !== undefined) clearTimeout(timer)
    timer = undefined
  }

  async function run(force: boolean): Promise<void> {
    const values = latest
    const serialized = JSON.stringify(values)
    if (options.validate && !options.validate(values)) {
      setStatus("idle")
      return
    }
    if (!force && serialized === saved) {
      setStatus(current === "pending" ? "saved" : current)
      return
    }
    setStatus("saving")
    let ok = false
    try {
      ok = (await options.save(values)) !== false
    } catch (err) {
      options.onError?.(err)
    }
    if (ok) saved = serialized
    setStatus(ok ? "saved" : "error")
  }

  async function flush(force = false): Promise<void> {
    clearTimer()
    if (saving) {
      // 保存期间又有变化：本次完成后再保存一次。
      queued = force || queued === "forced" ? "forced" : "changed"
      return saving
    }
    saving = run(force)
    try {
      await saving
    } finally {
      saving = undefined
    }
    const next = queued
    queued = undefined
    if (next) await flush(next === "forced")
  }

  return {
    change(values) {
      if (stopped) return
      latest = values
      clearTimer()
      setStatus("pending")
      timer = setTimeout(() => {
        timer = undefined
        void flush()
      }, delay)
    },
    flush,
    markSaved(values) {
      saved = JSON.stringify(values)
      latest = values
    },
    discard() {
      clearTimer()
      queued = undefined
      latest = JSON.parse(saved) as T
      if (current === "pending") setStatus("idle")
    },
    async stop() {
      if (stopped) return
      stopped = true
      if (timer !== undefined) await flush()
      else if (saving) await saving
    },
    resume() {
      stopped = false
    },
    status: () => current,
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
  }
}
