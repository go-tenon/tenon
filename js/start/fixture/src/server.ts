import { createServerEntry, createStartHandler } from "@tanstack/react-start/server"
import { printHandler } from "../../src/print"
import { streamHandler } from "../../src/server"
import { templates } from "./print"

const print = printHandler(templates)
const start = createStartHandler(streamHandler)

export default createServerEntry({ fetch: (request) => print(request) ?? start(request) })
