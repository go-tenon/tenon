// 用户菜单：头像，展开后是名称、应用的菜单项、配色和退出登录。作为布局的 user 参数：
//
//   <FrameLayout user={<UserMenu name={me.name} logout={api.auth.logout} />} />
//
// 布局可以往 user-menu 插槽放自己的开关，例如 FrameLayout 的“多标签”。

import {
  Avatar,
  Box,
  Center,
  Group,
  Indicator,
  type MantineColor,
  type MantineColorScheme,
  Menu,
  type MenuProps,
  SegmentedControl,
  Text,
  UnstyledButton,
  useMantineColorScheme,
  VisuallyHidden,
} from "@mantine/core"
import { IconDeviceDesktop, IconLogout, IconMoon, IconPalette, IconSun } from "@tabler/icons-react"
import { useNavigate } from "@tanstack/react-router"
import type { ReactNode } from "react"
import { useApp } from "../app/app"
import { Slot } from "../app/layout"
import { confirmDiscard } from "../app/tabs/tabs"
import { useText } from "../i18n/i18n"

/** 用户菜单中的一项。 */
export interface UserMenuItem {
  label: ReactNode
  icon?: ReactNode
  /** 应用内的地址。 */
  to?: string
  onClick?: () => void
}

/** UserMenu 的选项。 */
export interface UserMenuProps {
  /** 用户的名称：头像中显示首字，菜单顶部显示全名；读取中时为空。 */
  name?: string
  /** 用户的邮箱或账号，显示在菜单顶部的名称下面。 */
  email?: string
  /** 头像的地址；没有时显示名称的首字。 */
  avatar?: string
  /** 调用应用的退出接口：撤销登录、清除会话 cookie。之后清除会话中的身份。 */
  logout?: () => Promise<unknown>
  /** 菜单最上面的菜单项，例如“个人资料”。 */
  items?: UserMenuItem[]
  /** 菜单中的配色切换（亮色、暗色、跟随系统），默认显示；false 时不显示。 */
  colorScheme?: boolean
  /** 菜单弹出的方向，默认 bottom-end（顶栏右侧）；放在侧栏底部时用 right-end。 */
  position?: MenuProps["position"]
  /** 头像右下角的状态点颜色（例如在线用 green、离开用 gray）；不设置时不显示。 */
  statusColor?: MantineColor
}

/** 用户菜单：只显示头像；菜单顶部是用户的名称和邮箱，下面是应用的菜单项、配色、user-menu 插槽中的内容（例如 FrameLayout 的“多标签”开关）和退出登录。 */
export function UserMenu({
  name,
  email,
  avatar,
  logout,
  items = [],
  colorScheme = true,
  position = "bottom-end",
  statusColor,
}: UserMenuProps): ReactNode {
  const app = useApp()
  const t = useText()
  const navigate = useNavigate()
  const signOut = async () => {
    // 标签中有未保存的修改时先询问，用户选择留下时不退出。
    if (!(await confirmDiscard(app))) return
    try {
      await logout?.()
    } finally {
      app.session.signOut()
    }
  }
  return (
    <Menu width={220} position={position}>
      <Menu.Target>
        <UnstyledButton className="tn-avatar" aria-label={t("tenon.user_menu.label")}>
          <Indicator
            inline
            color={statusColor}
            size={9}
            offset={3}
            position="bottom-end"
            withBorder
            disabled={!statusColor}
          >
            <Avatar src={avatar} size={30} radius="xl" color="brand">
              {name?.slice(0, 1)}
            </Avatar>
          </Indicator>
        </UnstyledButton>
      </Menu.Target>
      <Menu.Dropdown p={0}>
        {name && (
          <Group gap="sm" wrap="nowrap" className="tn-menu-section tn-menu-user">
            <Avatar src={avatar} size={32} radius="xl" color="brand">
              {name.slice(0, 1)}
            </Avatar>
            <Box style={{ minWidth: 0 }}>
              <Text size="sm" fw={500} truncate>
                {name}
              </Text>
              {email && (
                <Text size="xs" c="dimmed" truncate>
                  {email}
                </Text>
              )}
            </Box>
          </Group>
        )}
        {items.length > 0 && (
          <div className="tn-menu-section">
            {items.map((item, i) => (
              <Menu.Item
                // biome-ignore lint/suspicious/noArrayIndexKey: 菜单项是固定的列表
                key={i}
                leftSection={item.icon}
                onClick={() => (item.to ? navigate({ to: item.to }) : item.onClick?.())}
              >
                {item.label}
              </Menu.Item>
            ))}
          </div>
        )}
        {colorScheme && <ColorSchemePicker />}
        <Slot name="user-menu" />
        <div className="tn-menu-section">
          <Menu.Item leftSection={<IconLogout size={18} stroke={1.5} />} onClick={() => void signOut()}>
            {t("tenon.user_menu.sign_out")}
          </Menu.Item>
        </div>
      </Menu.Dropdown>
    </Menu>
  )
}

// 三种配色：取值、显示文字的消息 ID 和图标。
const schemes = [
  { value: "light", label: "tenon.user_menu.light", icon: IconSun },
  { value: "dark", label: "tenon.user_menu.dark", icon: IconMoon },
  { value: "auto", label: "tenon.user_menu.auto", icon: IconDeviceDesktop },
] as const

/** 配色：一行“主题”，右边是亮色、暗色、跟随系统的三段切换（SegmentedControl）。切换不关闭菜单。 */
function ColorSchemePicker(): ReactNode {
  const { colorScheme, setColorScheme } = useMantineColorScheme()
  const t = useText()
  return (
    <Group className="tn-schemes" justify="space-between" gap="sm" wrap="nowrap">
      <Group gap="xs" wrap="nowrap">
        <IconPalette size={18} stroke={1.5} />
        <Text size="sm">{t("tenon.user_menu.theme")}</Text>
      </Group>
      <SegmentedControl
        size="xs"
        radius="xl"
        aria-label={t("tenon.user_menu.color_scheme")}
        value={colorScheme}
        onChange={(v) => setColorScheme(v as MantineColorScheme)}
        data={schemes.map(({ value, label, icon: SchemeIcon }) => ({
          value,
          label: (
            <Center>
              <SchemeIcon size={14} stroke={1.8} />
              <VisuallyHidden>{t(label)}</VisuallyHidden>
            </Center>
          ),
        }))}
      />
    </Group>
  )
}
