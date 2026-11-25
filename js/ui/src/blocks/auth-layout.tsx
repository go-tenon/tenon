// 登录页的布局：登录、注册、找回密码等页面的外框，表单放在其中。居中的卡片（CenteredAuthLayout）和左右分栏（SplitAuthLayout）
// 两种样式，与应用的布局、样式预设任意组合。

import { Group, Stack, Text, Title } from "@mantine/core"
import type { ReactNode } from "react"
import { formatDocumentTitle, useDocumentTitle } from "../app/document-title"

/**
 * 登录页的浏览器标题：title 和 brand 都是文字时是“标题 - 名称”，例如“登录 - 博客后台”；
 * 只有 title 是文字时只有标题；title 不是文字或 enabled 为 false 时不设置。
 */
function useAuthTitle(title: ReactNode, brand: ReactNode, enabled: boolean): void {
  const page = typeof title === "string" ? title : undefined
  const app = typeof brand === "string" ? brand : undefined
  useDocumentTitle(enabled && page ? formatDocumentTitle(page, app) : undefined)
}

/** CenteredAuthLayout 的选项。 */
export interface CenteredAuthLayoutProps {
  /** 卡片上方的标志图标，显示在 brand 左侧；传入独立 logo 时 brand 保持纯文字即可自动拼入浏览器标签标题。 */
  logo?: ReactNode
  /** 卡片上方的名称或标志。 */
  brand?: ReactNode
  title: ReactNode
  /** 标题下面的一句说明。 */
  description?: ReactNode
  /** 卡片下方，例如“还没有账号？注册”。 */
  footer?: ReactNode
  /** 浏览器标签的标题是“标题 - 名称”（title、brand 是文字时），默认开启；false 时不设置。 */
  documentTitle?: boolean
  children: ReactNode
}

/** 登录、注册、找回密码等页面的样式之一：页面中央一张卡片。 */
export function CenteredAuthLayout({
  logo,
  brand,
  title,
  description,
  footer,
  documentTitle = true,
  children,
}: CenteredAuthLayoutProps): ReactNode {
  useAuthTitle(title, brand, documentTitle)
  return (
    <div className="tn-auth">
      <Stack gap="lg" w="100%" maw={400} align="stretch">
        {(logo || brand) && (
          <Group justify="center" gap="xs">
            {logo}
            {brand}
          </Group>
        )}
        <div className="tn-auth-card">
          <Stack gap={4} mb="xl">
            <Title order={3}>{title}</Title>
            {description && (
              <Text c="dimmed" size="sm">
                {description}
              </Text>
            )}
          </Stack>
          {children}
        </div>
        {footer && (
          <Text ta="center" size="sm" c="dimmed">
            {footer}
          </Text>
        )}
      </Stack>
    </div>
  )
}

/** SplitAuthLayout 的选项。 */
export interface SplitAuthLayoutProps {
  /** 标志图标，显示在 brand 左侧；传入独立 logo 时 brand 保持纯文字即可自动拼入浏览器标签标题。 */
  logo?: ReactNode
  /** 名称或标志：宽屏时在左栏顶部，窄屏时在表单上方。 */
  brand?: ReactNode
  /** 左栏中间的内容，例如一句口号和几条产品介绍。 */
  aside?: ReactNode
  /** 左栏底部的一行小字，例如版权信息。 */
  asideFooter?: ReactNode
  title: ReactNode
  /** 标题下面的一句说明。 */
  description?: ReactNode
  /** 表单下方，例如“还没有账号？注册”。 */
  footer?: ReactNode
  /** 浏览器标签的标题是“标题 - 名称”（title、brand 是文字时），默认开启；false 时不设置。 */
  documentTitle?: boolean
  children: ReactNode
}

/**
 * 登录、注册、找回密码等页面的样式之一：左右分栏，左栏是品牌色的展示区，右栏是表单。
 * 窄屏时只显示表单，标志移到表单上方。
 */
export function SplitAuthLayout({
  logo,
  brand,
  aside,
  asideFooter,
  title,
  description,
  footer,
  documentTitle = true,
  children,
}: SplitAuthLayoutProps): ReactNode {
  useAuthTitle(title, brand, documentTitle)
  return (
    <div className="tn-auth-split">
      <aside className="tn-auth-split-aside">
        {(logo || brand) && (
          <Group gap="xs" className="tn-auth-split-brand">
            {logo}
            {brand}
          </Group>
        )}
        <div className="tn-auth-split-aside-body">{aside}</div>
        {asideFooter && (
          <Text size="xs" className="tn-auth-split-aside-footer">
            {asideFooter}
          </Text>
        )}
      </aside>
      <main className="tn-auth-split-main">
        <Stack gap="xl" w="100%" maw={380}>
          {(logo || brand) && (
            <Group hiddenFrom="md" gap="xs">
              {logo}
              {brand}
            </Group>
          )}
          <Stack gap={4}>
            <Title order={2}>{title}</Title>
            {description && (
              <Text c="dimmed" size="sm">
                {description}
              </Text>
            )}
          </Stack>
          {children}
          {footer && (
            <Text size="sm" c="dimmed">
              {footer}
            </Text>
          )}
        </Stack>
      </main>
    </div>
  )
}
