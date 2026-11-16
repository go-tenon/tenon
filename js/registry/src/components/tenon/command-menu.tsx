// ⌘K 命令面板：搜索并打开菜单中的页面。放在 search 插槽，按 ⌘K（Windows 上 Ctrl+K）打开。

import { type MenuItem, useNavigation } from "@go-tenon/shell"
import { useRouter } from "@tanstack/react-router"
import { Search } from "lucide-react"
import { type ReactNode, useEffect, useState } from "react"
import { MenuIcon } from "@/components/tenon/nav"
import { Button } from "@/components/ui/button"
import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"

/** 菜单中的全部页面，带上所在分组的标题。 */
function pages(items: readonly MenuItem[], group = ""): { item: MenuItem; group: string }[] {
  return items.flatMap((item) => (item.children ? pages(item.children, item.title) : [{ item, group }]))
}

/** ⌘K 命令面板。children 是面板中额外的命令组。 */
export function CommandMenu({ children }: { children?: ReactNode }): ReactNode {
  const [open, setOpen] = useState(false)
  const { menu } = useNavigation()
  const router = useRouter()
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault()
        setOpen((o) => !o)
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])
  const groups = new Map<string, MenuItem[]>()
  for (const { item, group } of pages(menu)) groups.set(group, [...(groups.get(group) ?? []), item])
  return (
    <>
      <Button variant="outline" size="sm" className="gap-2 text-muted-foreground" onClick={() => setOpen(true)}>
        <Search />
        搜索
        <kbd className="pointer-events-none rounded border bg-muted px-1.5 text-[10px]">⌘K</kbd>
      </Button>
      <CommandDialog open={open} onOpenChange={setOpen} title="搜索" description="搜索页面">
        <Command>
          <CommandInput placeholder="搜索页面…" />
          <CommandList>
            <CommandEmpty>没有找到。</CommandEmpty>
            {[...groups].map(([group, items]) => (
              <CommandGroup key={group || "_"} heading={group || "页面"}>
                {items.map((item) => (
                  <CommandItem
                    key={item.id}
                    value={`${group} ${item.title}`}
                    onSelect={() => {
                      setOpen(false)
                      void router.navigate({ href: item.path })
                    }}
                  >
                    <MenuIcon item={item} />
                    {item.title}
                  </CommandItem>
                ))}
              </CommandGroup>
            ))}
            {children}
          </CommandList>
        </Command>
      </CommandDialog>
    </>
  )
}
