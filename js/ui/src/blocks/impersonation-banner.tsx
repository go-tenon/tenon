// ImpersonationBanner：代为登录状态提示条，放在 RailLayout 或 FrameLayout 的 banner 插槽中。
// 使用 deep-orange 注意色，醒目标出当前操作者、目标成员、锁定租户与只读状态，并提供退出按钮。

import { Badge, Button, Group, Text } from "@mantine/core"
import { IconLogout, IconUserShield } from "@tabler/icons-react"
import type { ReactNode } from "react"
import { useText } from "../i18n/i18n"

/** ImpersonationBanner 的选项。 */
export interface ImpersonationBannerProps {
  /** 被代为登录的目标成员或用户名称。 */
  target: string
  /** 发起代为登录的操作者名称，可选。 */
  actor?: string
  /** 当前代为登录会话锁定的租户名称，可选。 */
  tenant?: string
  /** 当前代为登录会话是否限制为只读模式。 */
  readOnly?: boolean
  /** 退出代为登录的回调。 */
  onExit(): void | Promise<void>
  /** 退出按钮是否处于加载状态。 */
  exiting?: boolean
}

/** ImpersonationBanner 在页面顶部显示当前代为登录状态和退出操作。 */
export function ImpersonationBanner({
  target,
  actor,
  tenant,
  readOnly,
  onExit,
  exiting,
}: ImpersonationBannerProps): ReactNode {
  const t = useText()
  const message = actor
    ? t("tenon.impersonation.banner_with_actor", { actor, target })
    : t("tenon.impersonation.banner", { target })

  return (
    <div className="tn-impersonation-banner" role="status" aria-live="polite">
      <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
        <IconUserShield size={16} stroke={1.8} style={{ flexShrink: 0 }} />
        <Text size="xs" fw={600} truncate>
          {message}
        </Text>
        {tenant && (
          <Badge size="xs" variant="white" color="deep-orange" style={{ flexShrink: 0 }}>
            {tenant}
          </Badge>
        )}
        {readOnly && (
          <Badge size="xs" variant="outline" color="deep-orange" style={{ flexShrink: 0 }}>
            {t("tenon.impersonation.read_only")}
          </Badge>
        )}
      </Group>
      <Button
        size="compact-xs"
        variant="white"
        color="deep-orange"
        leftSection={<IconLogout size={13} stroke={1.8} />}
        loading={exiting}
        onClick={() => void onExit()}
        style={{ flexShrink: 0 }}
      >
        {t("tenon.impersonation.exit")}
      </Button>
    </div>
  )
}
