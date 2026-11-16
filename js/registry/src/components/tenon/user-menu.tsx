// 用户菜单：头像和名称，下拉中是颜色模式和退出。退出前，标签中有未保存的修改时先询问。

import { confirmDiscard, useApp } from "@go-tenon/shell"
import { LogOut, Monitor, Moon, Sun } from "lucide-react"
import type { ReactNode } from "react"
import { type ColorMode, useColorMode } from "@/components/tenon/color-mode"
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

/** UserMenu 的选项。 */
export interface UserMenuProps {
  name?: string
  email?: string
  avatar?: string
  /** 调用应用的登出接口（服务端撤销这次登录、清除 cookie）。之后清除会话，受保护的页面随之跳到登录页。 */
  logout?: () => Promise<unknown>
  /** 菜单中额外的项，排在颜色模式之前。 */
  children?: ReactNode
}

const modes: { value: ColorMode; label: string; icon: ReactNode }[] = [
  { value: "system", label: "跟随系统", icon: <Monitor /> },
  { value: "light", label: "亮色", icon: <Sun /> },
  { value: "dark", label: "暗色", icon: <Moon /> },
]

/** 用户菜单。放在 user 插槽：<Fill name="user"><UserMenu name={me.name} logout={logout} /></Fill> */
export function UserMenu({ name, email, avatar, logout, children }: UserMenuProps): ReactNode {
  const app = useApp()
  const { mode, setMode } = useColorMode()
  const signOut = async () => {
    if (!(await confirmDiscard(app))) return
    try {
      await logout?.()
    } finally {
      await app.session.signOut()
    }
  }
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        className="flex w-full items-center gap-2 rounded-md p-1.5 text-left text-sm hover:bg-muted"
        data-testid="user-menu"
      >
        <Avatar className="size-7">
          <AvatarImage src={avatar} alt={name} />
          <AvatarFallback>{name?.slice(0, 1) ?? "?"}</AvatarFallback>
        </Avatar>
        <span className="truncate group-data-[collapsible=icon]:hidden">{name}</span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-48">
        {(name || email) && (
          <DropdownMenuLabel>
            <div className="font-medium">{name}</div>
            {email && <div className="text-xs font-normal text-muted-foreground">{email}</div>}
          </DropdownMenuLabel>
        )}
        {children}
        <DropdownMenuSeparator />
        <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">颜色模式</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={mode} onValueChange={(v) => setMode(v as ColorMode)}>
          {modes.map((m) => (
            <DropdownMenuRadioItem key={m.value} value={m.value}>
              {m.icon}
              {m.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" onSelect={() => void signOut()}>
          <LogOut />
          退出登录
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
