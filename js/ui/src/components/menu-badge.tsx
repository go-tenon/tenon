// 菜单项的徽标：布局在菜单项上显示 useMenuBadge 登记的数字、文字或小红点。

import { Box, Text } from "@mantine/core"
import type { ReactNode } from "react"
import type { MenuItem } from "../app/menu"

/** MenuBadge 显示菜单项的徽标：数字和文字是红底白字的小圆角块，true 是小红点；没有徽标时不渲染。 */
export function MenuBadge({ value }: { value: MenuItem["badge"] }): ReactNode {
  if (value === undefined || value === false) return null
  if (value === true) return <Box w={6} h={6} bg="red.6" style={{ borderRadius: "50%" }} />
  return (
    <Text component="span" fz={10} fw={600} c="white" bg="red.6" px={5} lh="16px" style={{ borderRadius: 8 }}>
      {value}
    </Text>
  )
}
