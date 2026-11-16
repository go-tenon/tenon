// 对话框：确认（危险操作、未保存的修改）和新版本提示。

import { useNewVersion } from "@go-tenon/shell"
import { createContext, type ReactNode, useCallback, useContext, useRef, useState } from "react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"

/** confirm 的选项。 */
export interface ConfirmOptions {
  title: ReactNode
  description?: ReactNode
  /** 确认按钮的文字，默认“确定”。 */
  action?: string
  /** 危险操作（删除等）：确认按钮是红色的。 */
  destructive?: boolean
}

type Confirm = (options: ConfirmOptions | string) => Promise<boolean>

const ConfirmContext = createContext<Confirm | null>(null)

/**
 * 确认对话框，放在根路由中一次。之后在任何组件中 const confirm = useConfirm()；
 * 也可以交给 TabsOutlet 和 useUnsavedChanges 的 confirm，代替浏览器的确认框。
 */
export function ConfirmProvider({ children }: { children: ReactNode }): ReactNode {
  const [options, setOptions] = useState<ConfirmOptions>()
  const resolver = useRef<(ok: boolean) => void>(undefined)
  const confirm = useCallback<Confirm>(
    (o) =>
      new Promise<boolean>((resolve) => {
        resolver.current?.(false)
        resolver.current = resolve
        setOptions(typeof o === "string" ? { title: o } : o)
      }),
    [],
  )
  const close = (ok: boolean) => {
    resolver.current?.(ok)
    resolver.current = undefined
    setOptions(undefined)
  }
  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      <AlertDialog open={options !== undefined} onOpenChange={(open) => !open && close(false)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{options?.title}</AlertDialogTitle>
            {options?.description && <AlertDialogDescription>{options.description}</AlertDialogDescription>}
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={() => close(false)}>取消</AlertDialogCancel>
            <AlertDialogAction variant={options?.destructive ? "destructive" : "default"} onClick={() => close(true)}>
              {options?.action ?? "确定"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </ConfirmContext.Provider>
  )
}

/** 打开确认对话框，返回用户是否确认。不在 ConfirmProvider 之内时用浏览器的确认框。 */
export function useConfirm(): Confirm {
  const confirm = useContext(ConfirmContext)
  return confirm ?? ((o) => Promise.resolve(window.confirm(typeof o === "string" ? o : String(o.title ?? ""))))
}

/** 新版本提示：发布新版本后，提示打开着的页面刷新。放在根路由中一次。 */
export function NewVersionNotice(props: Parameters<typeof useNewVersion>[0] = {}): ReactNode {
  const { available, reload, dismiss } = useNewVersion(props)
  return (
    <AlertDialog open={available} onOpenChange={(open) => !open && dismiss()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>有新版本</AlertDialogTitle>
          <AlertDialogDescription>系统已经更新。先保存手头的修改，再刷新页面使用新版本。</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>稍后</AlertDialogCancel>
          <AlertDialogAction onClick={reload}>刷新</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
