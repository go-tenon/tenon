// 图标：路由元数据中的 icon 是名字（例如 "file-text"），这里映射为 antd 的图标。
// 只登记常用的几个，避免引入整个图标库；项目用 AdminProvider 的 icons 加上自己的。

import {
  AppstoreOutlined,
  BarChartOutlined,
  BellOutlined,
  DashboardOutlined,
  FileTextOutlined,
  FolderOutlined,
  HomeOutlined,
  MessageOutlined,
  SafetyOutlined,
  SettingOutlined,
  ShopOutlined,
  ShoppingCartOutlined,
  TeamOutlined,
  UserOutlined,
} from "@ant-design/icons"
import type { ReactNode } from "react"

/** 图标的名字到图标的映射。 */
export type IconMap = Record<string, ReactNode>

/** 默认登记的图标。 */
export const defaultIcons: IconMap = {
  appstore: <AppstoreOutlined />,
  "bar-chart": <BarChartOutlined />,
  bell: <BellOutlined />,
  dashboard: <DashboardOutlined />,
  "file-text": <FileTextOutlined />,
  folder: <FolderOutlined />,
  home: <HomeOutlined />,
  message: <MessageOutlined />,
  safety: <SafetyOutlined />,
  setting: <SettingOutlined />,
  shop: <ShopOutlined />,
  "shopping-cart": <ShoppingCartOutlined />,
  team: <TeamOutlined />,
  user: <UserOutlined />,
}
