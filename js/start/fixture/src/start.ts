import { createStart } from "@tanstack/react-start"
import { requestMiddleware } from "../../src/index"

/** 测试应用的 Start 实例，安装框架的请求中间件。 */
export const startInstance = createStart(() => ({ requestMiddleware: [requestMiddleware] }))
