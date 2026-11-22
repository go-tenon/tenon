// 已保存视图的状态对比：判断当前筛选与排序是否偏离已保存的配置，用于驱动“保存修改 / 另存为 / 重置”等交互。

import { useCallback, useMemo } from "react"

/**
 * 递归规范化视图配置值用于稳定比较：普通对象按键名排序并忽略值为 undefined 的键，
 * 保留 null、false、0、空字符串和空数组等显式语义值。
 */
export function canonicalizeViewConfig(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map((item) => canonicalizeViewConfig(item))
  }
  if (value !== null && typeof value === "object") {
    const out: Record<string, unknown> = {}
    const entries = Object.entries(value as Record<string, unknown>).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
    for (const [k, v] of entries) {
      if (v === undefined) continue
      out[k] = canonicalizeViewConfig(v)
    }
    return out
  }
  return value
}

/** 比较两份视图配置是否等价（不受对象字段顺序和 undefined 键影响）。 */
export function sameViewConfig(a: unknown, b: unknown): boolean {
  return JSON.stringify(canonicalizeViewConfig(a)) === JSON.stringify(canonicalizeViewConfig(b))
}

/** useSavedView 的选项。 */
export interface SavedViewOptions<T> {
  /** 可选业务归一化函数：在比较前整理配置（例如对无序数组排序、补全或剥离默认值）。 */
  normalize?: (value: T) => unknown
}

/** useSavedView 的返回值。 */
export interface SavedViewState<T> {
  /** 当前配置是否偏离已保存视图；saved 为 undefined 时恒为 false。 */
  dirty: boolean
  /** 按相同归一化规则判断任意两份配置是否等价。 */
  same(a: T, b: T): boolean
}

/**
 * 比较当前筛选状态 current 与已保存视图配置 saved，返回是否存在未保存修改（dirty）。
 * 适合配合 listSearch / useList 或自定义筛选栏驱动“保存更改 / 另存为 / 重置”状态条。
 */
export function useSavedView<T>(saved: T | undefined, current: T, options?: SavedViewOptions<T>): SavedViewState<T> {
  const normalize = options?.normalize
  const same = useCallback(
    (a: T, b: T) => sameViewConfig(normalize ? normalize(a) : a, normalize ? normalize(b) : b),
    [normalize],
  )
  const dirty = useMemo(() => {
    if (saved === undefined) return false
    return !same(saved, current)
  }, [saved, current, same])
  return useMemo(() => ({ dirty, same }), [dirty, same])
}
