// 页面外壳的 Content-Security-Policy，与 Go 托管前端时的 frontend.DefaultCSP 相同，另外允许连接实时频道。

/** 构建时写在页面外壳内联脚本上的占位 nonce，与 frontend.PrerenderNonce 相同。 */
export const PRERENDER_NONCE = "__tenon_csp_nonce__"

/**
 * 返回页面外壳的 CSP：脚本只能来自页面的来源，内联脚本必须带 nonce；样式允许内联；连接只能到页面的来源和 connect
 * 中的地址（实时频道的 wss:// 地址）。
 */
export function shellCSP(nonce: string, connect: string[]): string {
  return (
    `default-src 'self'; script-src 'self' 'nonce-${nonce}'; style-src 'self' 'unsafe-inline'; ` +
    `img-src 'self' data:; connect-src ${["'self'", ...connect].join(" ")}; base-uri 'none'; form-action 'self'; ` +
    "frame-ancestors 'none'; object-src 'none'"
  )
}

/** 把页面外壳中的占位 nonce 换成 nonce。 */
export function withNonce(html: string, nonce: string): string {
  return html.split(PRERENDER_NONCE).join(nonce)
}
