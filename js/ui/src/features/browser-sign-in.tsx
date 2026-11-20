// 在浏览器中登录：桌面端、App 的登录页上只有一个按钮，点击后在系统浏览器中登录，完成后读取身份并跳回登录前的页面。
// 放在 CenteredAuthLayout、SplitAuthLayout 等登录页的布局中，与 LoginForm 二选一。

import { Alert, Button, Stack, Text } from "@mantine/core"
import { IconAlertCircle, IconExternalLink } from "@tabler/icons-react"
import { type ReactNode, useState } from "react"
import { useApp } from "../app/app"
import { useText } from "../i18n/i18n"

/** BrowserSignIn 的选项。 */
export interface BrowserSignInProps {
  /** 打开系统浏览器登录，登录完成后返回；用户取消或超时时抛出错误。例如 host().auth.signIn。 */
  signIn: () => Promise<void>
  /** 登录前想去的页面（登录页的 ?redirect=），登录后回到这里。 */
  redirect?: string
}

/** 一个“在浏览器中登录”的按钮和说明；登录没有完成时显示提示，可以再试一次。 */
export function BrowserSignIn({ signIn, redirect }: BrowserSignInProps): ReactNode {
  const app = useApp()
  const t = useText()
  const [pending, setPending] = useState(false)
  const [failed, setFailed] = useState(false)
  const start = async () => {
    setPending(true)
    setFailed(false)
    try {
      await signIn()
      await app.session.signIn()
      await app.redirectAfterSignIn(redirect)
    } catch {
      setFailed(true)
    } finally {
      setPending(false)
    }
  }
  return (
    <Stack gap="md">
      {failed && (
        <Alert color="red" variant="light" icon={<IconAlertCircle size={18} />} role="alert">
          {t("tenon.browser_sign_in.failed")}
        </Alert>
      )}
      <Text size="sm" c="dimmed">
        {pending ? t("tenon.browser_sign_in.pending") : t("tenon.browser_sign_in.hint")}
      </Text>
      <Button loading={pending} onClick={start} rightSection={<IconExternalLink size={16} />} fullWidth autoFocus>
        {t("tenon.browser_sign_in.start")}
      </Button>
    </Stack>
  )
}
