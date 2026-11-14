// TestTSValidationMatchesServer 用 bun 运行它：同一批输入分别用 @go-tenon/client 的 validate 和示例服务校验，
// 比较两边的位置、错误码和提示。BASE_URL 是示例服务的 API 地址，EXAMPLE 是生成的示例客户端目录，LIB 是 @go-tenon/client 的入口。

type Lib = typeof import("../../../../js/client/src/index")
type Schemas = typeof import("../internal/example/ts/schemas")
type Messages = typeof import("../internal/example/ts/messages")

const lib = (await import(process.env.LIB!)) as Lib
const { schemas, components } = (await import(`${process.env.EXAMPLE}/schemas.ts`)) as Schemas
const { messages } = (await import(`${process.env.EXAMPLE}/messages.ts`)) as Messages
const baseURL = process.env.BASE_URL!

type Row = [string, string, string]

async function server(method: string, path: string, body?: unknown): Promise<Row[]> {
  const res = await fetch(baseURL + path, {
    method,
    headers: { "Content-Type": "application/json", "Accept-Language": "zh-CN", Authorization: "Bearer secret" },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.ok) return []
  const problem = (await res.json()) as { errors?: { location: string; code: string; message: string }[] }
  return rows(problem.errors ?? [])
}

function rows(errors: { location?: string; code?: string; message: string }[]): Row[] {
  return errors.map((e): Row => [e.location ?? "", e.code ?? "", e.message]).sort((a, b) => (a.join() < b.join() ? -1 : 1))
}

function client(schema: object, value: unknown, location: string): Row[] {
  return rows(lib.validate(schema, value, { components, messages, language: "zh-CN", location }))
}

let failed = false
function compare(name: string, got: Row[], want: Row[]): void {
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    failed = true
    console.error(`FAIL ${name}\n  client: ${JSON.stringify(got)}\n  server: ${JSON.stringify(want)}`)
  }
}

const bodies: Record<string, unknown> = {
  "missing fields": {},
  "empty title and bad slug": { title: "", slug: "Hello World" },
  "title too long": { title: "长".repeat(201), slug: "ok" },
  "wrong types": { title: 5, slug: true },
  "bad array item": { title: "ok", slug: "ok", tags: ["a", 1] },
  "unexpected field": { title: "ok", slug: "ok", extra: true },
  "null tags": { title: "ok", slug: "null-tags", tags: null },
  "case-insensitive keys": { Title: "ok", SLUG: "case-keys" },
  valid: { title: "标题", slug: "valid-one", tags: ["go"] },
}
for (const [name, body] of Object.entries(bodies)) {
  compare(`body: ${name}`, client(schemas.article.create.body, body, "body"), await server("POST", "/articles", body))
}

const queries: Record<string, Record<string, unknown>> = {
  "per_page too large": { per_page: 500 },
  "page too small": { page: 0 },
  "unknown status": { status: "archived" },
  valid: { page: 2, per_page: 10, status: "draft" },
}
for (const [name, query] of Object.entries(queries)) {
  const qs = new URLSearchParams(Object.entries(query).map(([k, v]) => [k, String(v)])).toString()
  compare(`query: ${name}`, client(schemas.article.list.query, query, "query"), await server("GET", `/articles?${qs}`))
}

if (failed) process.exit(1)
console.log("validation matches")
