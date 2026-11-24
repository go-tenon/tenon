import { describe, expect, it } from "vitest"
import { grants } from "./permissions"

describe("grants", () => {
  // 与 identity/subject_test.go 的 TestGrants 是同一张表：前端和服务端按同样的规则匹配。
  it.each([
    [["articles:write"], "articles:write", true],
    [["articles:write"], "articles:read", false],
    [["articles:*"], "articles:read", true],
    [["articles:*"], "articles:comments:delete", true],
    [["articles:*"], "articles", false],
    [["articles:*"], "articlesx:read", false],
    [["articles:comments:*"], "articles:read", false],
    [["*:read"], "articles:read", true],
    [["*:read"], "articles:comments:read", true],
    [["*:read"], "articles:write", false],
    [["*:read"], "articles:unread", false],
    [["*:read"], "read", false],
    [["*:read"], ":read", false],
    [["*:"], "articles:", false],
    [[":*"], ":read", false],
    [["*"], "members:manage", true],
    [["articles*"], "articles:read", false],
    [[], "articles:read", false],
    [["*"], "", false],
  ])("grants(%j, %j) = %s", (granted, perm, want) => {
    expect(grants(granted as string[], perm as string)).toBe(want)
  })

  it("接受 Set", () => {
    expect(grants(new Set(["providers:*"]), "providers:read")).toBe(true)
  })
})
