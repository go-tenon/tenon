import { createFileRoute } from "@tanstack/react-router"

export const Route = createFileRoute("/")({ component: () => <h1>预渲染的首页</h1> })
