// 登录后的回跳地址：只接受同源的相对路径，防止开放重定向。

/**
 * 返回可以安全跳转的地址：redirect 是以单个 / 开头、解析后仍在同一来源的相对路径时返回它（路径、查询和片段），
 * 否则返回 fallback。
 */
export function safeRedirect(redirect: unknown, fallback = "/"): string {
  if (
    typeof redirect !== "string" ||
    !redirect.startsWith("/") ||
    redirect.startsWith("//") ||
    redirect.includes("\\")
  ) {
    return fallback
  }
  const base = "http://tenon.invalid"
  try {
    const url = new URL(redirect, base)
    if (url.origin !== base) return fallback
    return `${url.pathname}${url.search}${url.hash}`
  } catch {
    return fallback
  }
}
