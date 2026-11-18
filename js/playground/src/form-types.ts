// 只参与类型检查，不在页面中使用：生成的客户端中请求体是 interface，useSchemaForm 要能直接接受它，
// 包括嵌套的 interface。约束改得比 @mantine/form 的 useForm 更严时，这里的类型检查失败。
import type { Schema } from "@go-tenon/client"
import { useSchemaForm } from "@go-tenon/ui"

interface Endpoint {
  base_url: string
  api_key: string
}

interface Changes {
  name: string
  translation: Endpoint
}

export function useChangesForm(schema: Schema) {
  return useSchemaForm({ schema, initialValues: { name: "", translation: { base_url: "", api_key: "" } } as Changes })
}
