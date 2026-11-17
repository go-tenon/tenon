import { createFileRoute } from "@tanstack/react-router"

/** 预渲染的首页。 */
export const Route = createFileRoute("/")({ component: () => <h1>预渲染的首页</h1> })
