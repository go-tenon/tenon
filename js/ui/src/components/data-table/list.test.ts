import { describe, expect, it } from "vitest"
import { listSearch, tabbedListSearch } from "./list"

describe("tabbedListSearch", () => {
  const membersSearch = listSearch({
    type: "object",
    properties: {
      page: { type: "integer", default: 1 },
      q: { type: "string" },
      status: { type: "string", enum: ["online", "away"] },
    },
  })
  const rolesSearch = listSearch({
    type: "object",
    properties: {
      page: { type: "integer", default: 1 },
      q: { type: "string" },
    },
  })

  const validate = tabbedListSearch({
    members: membersSearch,
    roles: rolesSearch,
    attributes: undefined,
  })

  it("默认标签省略 tab 参数，只保留对应列表的合法查询参数", () => {
    expect(validate({ tab: "members", q: "alice", status: "online", page: "1", extra: "x" })).toEqual({
      q: "alice",
      status: "online",
    })
    expect(validate({ tab: "unknown", status: "away", page: "2" })).toEqual({
      status: "away",
      page: 2,
    })
  })

  it("切换到其他标签时保留 tab 并滤掉不属于该标签的查询参数", () => {
    expect(validate({ tab: "roles", q: "admin", status: "online", page: "2" })).toEqual({
      tab: "roles",
      q: "admin",
      page: 2,
    })
  })

  it("非列表标签只保留 tab 参数", () => {
    expect(validate({ tab: "attributes", q: "vip", page: "3" })).toEqual({ tab: "attributes" })
  })
})
