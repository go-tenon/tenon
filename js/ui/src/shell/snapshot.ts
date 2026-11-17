// 身份的服务端快照：服务端渲染时不读取会话，页面按未登录渲染；浏览器水合时用同样的值，之后再按会话更新，
// 两边的 HTML 一致。

/** 服务端快照中的登录状态：未登录。 */
export const signedOut = (): boolean => false
/** 服务端快照中的账号：空。 */
export const noAccount = (): string => ""
/** 服务端快照中的租户：没有。 */
export const noTenant = (): string | undefined => undefined
