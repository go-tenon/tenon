// 转发到服务器时请求头和响应头的处理。

/** 页面发出的请求中不转发的请求头：来源和 cookie 属于宿主中的页面，Authorization 和 X-App-Version 由宿主设置。 */
const droppedRequestHeaders = new Set(["origin", "referer", "cookie", "host", "authorization", "x-app-version"])

/** 返回转发给服务器的请求头：去掉页面的来源和 cookie，加上宿主保存的 token 和客户端版本。 */
export function requestHeaders(from: Headers, token: string | undefined, appVersion: string): Headers {
  const out = new Headers()
  from.forEach((value, name) => {
    if (!droppedRequestHeaders.has(name.toLowerCase())) out.set(name, value)
  })
  if (token) out.set("Authorization", `Bearer ${token}`)
  out.set("X-App-Version", appVersion)
  return out
}

/** 返回交给页面的响应头：去掉 Set-Cookie，宿主中的页面不用 cookie。 */
export function responseHeaders(from: Headers): Headers {
  const out = new Headers()
  from.forEach((value, name) => {
    if (name.toLowerCase() !== "set-cookie") out.append(name, value)
  })
  return out
}

/** 报告状态码 status 的响应是否不能带响应体。 */
export function nullBodyStatus(status: number): boolean {
  return status === 101 || status === 204 || status === 205 || status === 304
}
