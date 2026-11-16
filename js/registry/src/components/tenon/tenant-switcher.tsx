// 租户切换：当前租户和全部租户，见 @go-tenon/shell 的 useTenant 和 createApp 的 tenancy。

import { useTenant } from "@go-tenon/shell"
import { Check, ChevronsUpDown } from "lucide-react"
import type { ReactNode } from "react"
import { Button } from "@/components/ui/button"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"

/** 租户切换，放在 tenant 插槽。只有一个租户时只显示名称，没有租户时不显示。 */
export function TenantSwitcher(): ReactNode {
  const { tenant, tenants, current, switchTo } = useTenant()
  if (!current) return null
  const label = tenant?.name ?? current
  if (tenants.length <= 1) return <span className="text-sm text-muted-foreground">{label}</span>
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" data-testid="tenant-switcher">
          {label}
          <ChevronsUpDown className="opacity-50" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {tenants.map((t) => (
          <DropdownMenuItem key={t.id} onSelect={() => void switchTo(t.id)}>
            {t.name}
            {t.id === current && <Check className="ml-auto" />}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
