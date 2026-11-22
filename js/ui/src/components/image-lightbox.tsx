// 图片灯箱：全屏暗底查看原图，支持下载、在新标签页打开原图和按 Esc / 点击遮罩关闭。

import { ActionIcon, Group, Modal, Stack, Text, Tooltip } from "@mantine/core"
import { IconDownload, IconExternalLink, IconX } from "@tabler/icons-react"
import type { ReactNode } from "react"
import { useText } from "../i18n/i18n"

/** ImageLightbox 的属性。 */
export interface ImageLightboxProps {
  /** 要预览的图片地址；为空时灯箱关闭。 */
  src: string | null
  /** 图片标题或原始文件名，显示在底部并用于下载文件名。 */
  filename?: string
  /** 关闭灯箱。 */
  onClose: () => void
}

/** 点击缩略图后弹出大图预览，点击背景或按 Esc 关闭。 */
export function ImageLightbox({ src, filename, onClose }: ImageLightboxProps): ReactNode {
  const t = useText()
  return (
    <Modal
      opened={!!src}
      onClose={onClose}
      centered
      size="auto"
      padding="xs"
      withCloseButton={false}
      radius="md"
      overlayProps={{ backgroundOpacity: 0.72, blur: 2 }}
      styles={{
        content: { background: "transparent", boxShadow: "none" },
        body: { padding: 0 },
      }}
    >
      {src && (
        <Stack gap="xs" align="center">
          <Group justify="space-between" w="100%" wrap="nowrap" gap="md">
            <Text size="sm" c="var(--mantine-color-white)" truncate style={{ maxWidth: "60vw" }}>
              {filename ?? ""}
            </Text>
            <Group gap={6} wrap="nowrap">
              <Tooltip label={t("tenon.lightbox.open_original")}>
                <ActionIcon
                  component="a"
                  href={src}
                  target="_blank"
                  rel="noopener noreferrer"
                  variant="filled"
                  color="dark"
                  aria-label={t("tenon.lightbox.open_original")}
                >
                  <IconExternalLink size={16} />
                </ActionIcon>
              </Tooltip>
              <Tooltip label={t("tenon.lightbox.download")}>
                <ActionIcon
                  component="a"
                  href={src}
                  download={filename || true}
                  target="_blank"
                  rel="noopener noreferrer"
                  variant="filled"
                  color="dark"
                  aria-label={t("tenon.lightbox.download")}
                >
                  <IconDownload size={16} />
                </ActionIcon>
              </Tooltip>
              <Tooltip label={t("tenon.lightbox.close")}>
                <ActionIcon variant="filled" color="dark" onClick={onClose} aria-label={t("tenon.lightbox.close")}>
                  <IconX size={16} />
                </ActionIcon>
              </Tooltip>
            </Group>
          </Group>
          <img
            src={src}
            alt={filename ?? ""}
            style={{
              maxWidth: "90vw",
              maxHeight: "82vh",
              objectFit: "contain",
              borderRadius: "var(--mantine-radius-md)",
              display: "block",
            }}
          />
        </Stack>
      )}
    </Modal>
  )
}
