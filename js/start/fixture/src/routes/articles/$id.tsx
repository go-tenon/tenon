import { createFileRoute } from "@tanstack/react-router"
import { apiFetch } from "../../../../src/index"

type Echo = { path: string; cookie: string; requestId: string; traceparent: string }

export const Route = createFileRoute("/articles/$id")({
  loader: async ({ params }) => (await apiFetch(`/api/articles/${params.id}`)).json() as Promise<Echo>,
  component: Article,
})

function Article() {
  const echo = Route.useLoaderData()
  return <pre data-testid="echo">{JSON.stringify(echo)}</pre>
}
