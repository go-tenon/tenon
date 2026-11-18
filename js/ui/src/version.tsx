// 新版本提示：部署了新版本后，已经打开的页面还在运行旧代码。useNewVersion（shell/version.ts）发现版本号变了时，
// 右下角提示刷新。FrameLayout 已经包含它；其他布局（例如官网）需要时自己放一个。

import { Affix, Button, Group, Paper, Text, Transition } from "@mantine/core"
import type { ReactNode } from "react"
import { useNewVersion } from "./shell"

/** NewVersionNotice 的选项，交给 useNewVersion；测试中用来指定版本和地址。 */
export interface NewVersionNoticeProps {
  current?: string
  url?: string
  interval?: number
  fetch?: typeof fetch
}

/** 有新版本时在右下角提示：“刷新”载入新版本，“稍后”本次打开页面期间不再提示。 */
export function NewVersionNotice(props: NewVersionNoticeProps): ReactNode {
  const v = useNewVersion(props)
  return (
    <Affix position={{ bottom: 16, right: 16 }}>
      <Transition mounted={v.available} transition="slide-up">
        {(style) => (
          <Paper style={style} withBorder shadow="md" p="sm" radius="md" role="status">
            <Text size="sm">有新版本可用，刷新后使用。</Text>
            <Group gap="xs" mt="xs" justify="flex-end">
              <Button size="xs" variant="default" onClick={v.dismiss}>
                稍后
              </Button>
              <Button size="xs" onClick={v.reload}>
                刷新
              </Button>
            </Group>
          </Paper>
        )}
      </Transition>
    </Affix>
  )
}
