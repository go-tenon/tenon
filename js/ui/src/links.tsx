// 类型安全的路由链接：用 TanStack Router 的 createLink 包装 Mantine 的组件，to、params、search 按路由表检查。
// 直接写 <Button component={Link} to=…> 会丢掉路由参数的类型。

import {
  ActionIcon,
  type ActionIconProps,
  Anchor,
  type AnchorProps,
  Button,
  type ButtonProps,
  NavLink,
  type NavLinkProps,
} from "@mantine/core"
import { createLink } from "@tanstack/react-router"
import { type AnchorHTMLAttributes, forwardRef } from "react"

type AnchorAttrs = Omit<AnchorHTMLAttributes<HTMLAnchorElement>, "color" | "style">

/** 按钮样式的链接：<ButtonLink to="/articles/$id" params={{ id }}>查看</ButtonLink> */
export const ButtonLink = createLink(
  forwardRef<HTMLAnchorElement, ButtonProps & AnchorAttrs>(function ButtonAnchor(props, ref) {
    return <Button component="a" ref={ref} {...props} />
  }),
)

/** 文字链接。 */
export const AnchorLink = createLink(
  forwardRef<HTMLAnchorElement, AnchorProps & AnchorAttrs>(function TextAnchor(props, ref) {
    return <Anchor component="a" ref={ref} {...props} />
  }),
)

/** 导航项链接，用于侧栏、文档目录。 */
export const NavLinkLink = createLink(
  forwardRef<HTMLAnchorElement, NavLinkProps & AnchorAttrs>(function NavAnchor(props, ref) {
    return <NavLink component="a" ref={ref} {...props} />
  }),
)

/** 图标按钮样式的链接。 */
export const ActionIconLink = createLink(
  forwardRef<HTMLAnchorElement, ActionIconProps & AnchorAttrs>(function IconAnchor(props, ref) {
    return <ActionIcon component="a" ref={ref} {...props} />
  }),
)
