import { createStart } from "@tanstack/react-start"
import { requestMiddleware } from "../../src/index"

export const startInstance = createStart(() => ({ requestMiddleware: [requestMiddleware] }))
