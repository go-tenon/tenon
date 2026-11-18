import { describe, expect, it, vi } from "vitest"
import {
  type CryptoLike,
  createAuthorization,
  exchangeCode,
  type OAuthError,
  readCallback,
  revokeToken,
} from "./authorize"
import type { FetchLike, ResponseLike } from "./fetch"

const issuer = "https://example.com"
const redirectUri = "http://127.0.0.1:53123/callback"

function json(status: number, body: unknown): ResponseLike {
  return { ok: status < 300, status, headers: { get: () => null }, json: async () => body, text: async () => "" }
}

// RFC 7636 附录 B 的例子：32 个随机字节、对应的 code_verifier 和 code_challenge。
const rfcBytes = [
  116, 24, 223, 180, 151, 153, 224, 37, 79, 250, 96, 125, 216, 173, 187, 186, 22, 212, 37, 77, 105, 214, 191, 240, 91,
  88, 5, 88, 83, 132, 141, 121,
]
const rfcVerifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
const rfcChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"

/** 运行环境的 Web Crypto：随机数换成 state 用的 16 个 0x01，再给 verifier 用 RFC 的字节。 */
function rfcCrypto(): CryptoLike {
  const real = (globalThis as unknown as { crypto: CryptoLike }).crypto
  const fills = [new Array(16).fill(1), rfcBytes]
  return {
    getRandomValues: (array) => {
      array.set(fills.shift() ?? [])
      return array
    },
    subtle: real.subtle,
  }
}

/** 读出查询串的参数。 */
function query(s: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const pair of s.slice(s.indexOf("?") + 1).split("&")) {
    const [k, v] = pair.split("=")
    out[decodeURIComponent(k)] = decodeURIComponent(v)
  }
  return out
}

describe("createAuthorization", () => {
  it("按 RFC 7636 生成 verifier 和 S256 的 challenge，授权地址不带范围和资源", async () => {
    const auth = await createAuthorization({ issuer, clientId: "admin-desktop", redirectUri, crypto: rfcCrypto() })
    expect(auth.verifier).toBe(rfcVerifier)
    expect(auth.state).toBe("AQEBAQEBAQEBAQEBAQEBAQ")
    expect(auth.url.startsWith("https://example.com/oauth/authorize?")).toBe(true)
    expect(query(auth.url)).toEqual({
      response_type: "code",
      client_id: "admin-desktop",
      redirect_uri: redirectUri,
      state: auth.state,
      code_challenge: rfcChallenge,
      code_challenge_method: "S256",
    })
    expect(auth.redirectUri).toBe(redirectUri)
  })

  it("默认用运行环境的 Web Crypto，每次生成不同的 state 和 verifier", async () => {
    const a = await createAuthorization({ issuer, clientId: "c", redirectUri })
    const b = await createAuthorization({ issuer, clientId: "c", redirectUri })
    expect(a.verifier).toMatch(/^[A-Za-z0-9_-]{43}$/)
    expect(a.state).not.toBe(b.state)
    expect(a.verifier).not.toBe(b.verifier)
  })
})

describe("readCallback", () => {
  const auth = { state: "s1" }

  it("返回授权码", () => {
    expect(readCallback(`${redirectUri}?code=abc%2B1&state=s1&iss=${encodeURIComponent(issuer)}`, auth, issuer)).toBe(
      "abc+1",
    )
  })

  it("state 不同、带着错误、iss 不对或没有授权码时抛出 OAuthError", () => {
    const code = (url: string) => {
      try {
        readCallback(url, auth, issuer)
      } catch (err) {
        return (err as OAuthError).code
      }
    }
    expect(code(`${redirectUri}?code=abc&state=other`)).toBe("state_mismatch")
    expect(code(`${redirectUri}?error=access_denied&error_description=the+user+denied&state=s1`)).toBe("access_denied")
    expect(code(`${redirectUri}?code=abc&state=s1&iss=https%3A%2F%2Fevil.example`)).toBe("issuer_mismatch")
    expect(code(`${redirectUri}?state=s1`)).toBe("invalid_request")
  })
})

describe("exchangeCode", () => {
  it("用授权码和 verifier 换取 token", async () => {
    const fetch = vi.fn<FetchLike>(async () => json(200, { access_token: "tok", token_type: "Bearer" }))
    const token = await exchangeCode({
      issuer,
      clientId: "admin-desktop",
      auth: { verifier: "v", redirectUri },
      code: "c1",
      fetch,
    })
    expect(token).toBe("tok")
    const [url, init] = fetch.mock.calls[0]
    expect(url).toBe("https://example.com/oauth/token")
    expect(init?.method).toBe("POST")
    expect(query(`?${String(init?.body)}`)).toEqual({
      grant_type: "authorization_code",
      code: "c1",
      client_id: "admin-desktop",
      redirect_uri: redirectUri,
      code_verifier: "v",
    })
  })

  it("令牌端点的错误变成 OAuthError", async () => {
    const fetch: FetchLike = async () =>
      json(400, { error: "invalid_grant", error_description: "the code has expired" })
    await expect(
      exchangeCode({ issuer, clientId: "c", auth: { verifier: "v", redirectUri }, code: "c1", fetch }),
    ).rejects.toMatchObject({ name: "OAuthError", code: "invalid_grant", description: "the code has expired" })
  })
})

describe("revokeToken", () => {
  it("把 token 交回撤销端点", async () => {
    const fetch = vi.fn<FetchLike>(async () => json(200, {}))
    await revokeToken({ issuer, token: "tok", fetch })
    expect(fetch.mock.calls[0][0]).toBe("https://example.com/oauth/revoke")
    expect(String(fetch.mock.calls[0][1]?.body)).toBe("token=tok")
  })
})
