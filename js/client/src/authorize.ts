// 原生客户端（桌面端、App）在系统浏览器中登录：OAuth 授权码 + PKCE，见客户端协议的“原生客户端”。
// 这里只有与界面和平台无关的部分：生成授权地址、读取回调、换取和交回 token。打开浏览器、接收回调、
// 保存 token 由各客户端自己完成，例如 Electron 的主进程。不依赖 DOM 和 Node：URL、crypto 都按最小形状声明。

import { type FetchLike, globalFetch } from "./fetch"

/** 授权端点和令牌端点的路径，接在 issuer 之后。 */
export const AUTHORIZE_PATH = "/oauth/authorize"
export const TOKEN_PATH = "/oauth/token"
export const REVOKE_PATH = "/oauth/revoke"

/** Web Crypto 中用到的部分：浏览器、Node、Bun、Electron 的 globalThis.crypto 都符合。 */
export interface CryptoLike {
  getRandomValues<T extends Uint8Array>(array: T): T
  subtle: { digest(algorithm: "SHA-256", data: Uint8Array): Promise<ArrayBuffer> }
}

/** 一次登录的参数，在打开浏览器之前生成，收到回调之前由客户端保存。 */
export interface Authorization {
  url: string // 在系统浏览器中打开的授权地址
  state: string // 随机串，回调中必须带着同一个值
  verifier: string // PKCE 的 code_verifier，换取 token 时交给令牌端点
  redirectUri: string // 本次的回调地址
}

/** createAuthorization 的参数。 */
export interface AuthorizationOptions {
  issuer: string // 授权服务器的地址，例如 https://example.com，不带结尾的 /
  clientId: string // 在授权服务器中登记的客户端 ID
  redirectUri: string // 本次的回调地址，例如 http://127.0.0.1:53123/callback
  crypto?: CryptoLike // 默认是 globalThis.crypto
}

/** 授权或换取 token 失败。code 是 OAuth 的错误码，例如 access_denied、invalid_grant，或本模块的 state_mismatch、issuer_mismatch。 */
export class OAuthError extends Error {
  readonly code: string
  readonly description?: string

  constructor(code: string, description?: string) {
    super(description ? `${code}: ${description}` : code)
    this.name = "OAuthError"
    this.code = code
    this.description = description
  }
}

/** 生成一次登录的参数：随机的 state 和 code_verifier，以及带 S256 code_challenge 的授权地址。授权请求不带范围和资源。 */
export async function createAuthorization(options: AuthorizationOptions): Promise<Authorization> {
  const crypto = options.crypto ?? globalCrypto()
  const state = base64url(crypto.getRandomValues(new Uint8Array(16)))
  const verifier = base64url(crypto.getRandomValues(new Uint8Array(32))) // 43 个字符
  const challenge = base64url(new Uint8Array(await crypto.subtle.digest("SHA-256", ascii(verifier))))
  const query = encodeQuery({
    response_type: "code",
    client_id: options.clientId,
    redirect_uri: options.redirectUri,
    state,
    code_challenge: challenge,
    code_challenge_method: "S256",
  })
  return { url: `${options.issuer}${AUTHORIZE_PATH}?${query}`, state, verifier, redirectUri: options.redirectUri }
}

/**
 * 读取回调地址 callbackUrl 中的授权码。回调带着 error（例如用户拒绝时的 access_denied）、state 与 auth.state 不同、
 * iss 不是 issuer 时抛出 OAuthError。
 */
export function readCallback(callbackUrl: string, auth: Pick<Authorization, "state">, issuer: string): string {
  const q = parseQuery(callbackUrl)
  if (q.state !== auth.state) throw new OAuthError("state_mismatch", "the callback does not belong to this sign-in")
  if (q.error) throw new OAuthError(q.error, q.error_description)
  if (q.iss !== undefined && q.iss !== issuer)
    throw new OAuthError("issuer_mismatch", `the callback came from ${q.iss}`)
  if (!q.code) throw new OAuthError("invalid_request", "the callback has no code")
  return q.code
}

/** exchangeCode 的参数。 */
export interface ExchangeOptions {
  issuer: string
  clientId: string
  auth: Pick<Authorization, "verifier" | "redirectUri">
  code: string // readCallback 读出的授权码
  fetch?: FetchLike // 默认是全局的 fetch
}

/** 用授权码和 code_verifier 换取登录 token，返回 token 明文。令牌端点返回错误时抛出 OAuthError。 */
export async function exchangeCode(options: ExchangeOptions): Promise<string> {
  const send = options.fetch ?? globalFetch()
  const res = await send(`${options.issuer}${TOKEN_PATH}`, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
    body: encodeQuery({
      grant_type: "authorization_code",
      code: options.code,
      client_id: options.clientId,
      redirect_uri: options.auth.redirectUri,
      code_verifier: options.auth.verifier,
    }),
  })
  const body = (await res.json().catch(() => ({}))) as Record<string, unknown>
  if (!res.ok || typeof body.access_token !== "string") {
    throw new OAuthError(
      typeof body.error === "string" ? body.error : `http_${res.status}`,
      typeof body.error_description === "string" ? body.error_description : undefined,
    )
  }
  return body.access_token
}

/** 把 token 交回授权服务器，撤销这次登录（RFC 7009）。token 已经无效时同样成功。 */
export async function revokeToken(options: { issuer: string; token: string; fetch?: FetchLike }): Promise<void> {
  const send = options.fetch ?? globalFetch()
  const res = await send(`${options.issuer}${REVOKE_PATH}`, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: encodeQuery({ token: options.token }),
  })
  if (!res.ok) throw new OAuthError(`http_${res.status}`, "revoking the token failed")
}

/** 返回全局的 crypto；没有 Web Crypto 时抛出错误，提示传入一个。 */
function globalCrypto(): CryptoLike {
  const c = (globalThis as { crypto?: CryptoLike }).crypto
  if (!c?.subtle) throw new Error("@go-tenon/client: no Web Crypto; pass crypto in the options")
  return c
}

/** 把 ASCII 字符串转为字节。 */
function ascii(s: string): Uint8Array {
  const out = new Uint8Array(s.length)
  for (let i = 0; i < s.length; i++) out[i] = s.charCodeAt(i)
  return out
}

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

/** 无填充的 base64url 编码。 */
function base64url(bytes: Uint8Array): string {
  let out = ""
  for (let i = 0; i < bytes.length; i += 3) {
    const n = (bytes[i] << 16) | ((bytes[i + 1] ?? 0) << 8) | (bytes[i + 2] ?? 0)
    out += alphabet[(n >> 18) & 63] + alphabet[(n >> 12) & 63]
    if (i + 1 < bytes.length) out += alphabet[(n >> 6) & 63]
    if (i + 2 < bytes.length) out += alphabet[n & 63]
  }
  return out
}

/** 编码为 application/x-www-form-urlencoded。 */
function encodeQuery(params: Record<string, string>): string {
  return Object.entries(params)
    .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v)}`)
    .join("&")
}

/** 读出地址中查询串的参数，同名参数取第一个。 */
function parseQuery(url: string): Record<string, string> {
  const start = url.indexOf("?")
  const end = url.indexOf("#")
  const query = start < 0 ? "" : url.slice(start + 1, end > start ? end : undefined)
  const out: Record<string, string> = {}
  for (const pair of query.split("&")) {
    if (!pair) continue
    const eq = pair.indexOf("=")
    const decode = (s: string) => decodeURIComponent(s.replace(/\+/g, " "))
    const key = decode(eq < 0 ? pair : pair.slice(0, eq))
    if (!(key in out)) out[key] = eq < 0 ? "" : decode(pair.slice(eq + 1))
  }
  return out
}
