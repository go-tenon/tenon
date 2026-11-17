import { createServerEntry, createStartHandler } from "@tanstack/react-start/server"
import { streamHandler } from "../../src/server"

export default createServerEntry({ fetch: createStartHandler(streamHandler) })
