// 宿主中页面请求的去向：转发到服务器、安装包中的文件、页面外壳，或者不存在。

import { existsSync, statSync } from "node:fs"
import { resolve, sep } from "node:path"

/** 宿主中一个页面请求的去向。 */
export type Route =
  | { kind: "proxy" } // 转发到服务器，例如 /api/*
  | { kind: "file"; path: string } // 安装包中前端构建出的文件
  | { kind: "shell" } // 前端路由中的页面：返回页面外壳，由前端路由渲染
  | { kind: "missing" } // 不存在：base 以外的路径、带扩展名但找不到的文件、越出前端目录的路径

/** route 的参数。 */
export interface RouteOptions {
  root: string // 前端按 SPA 模式构建的目录
  base: string // 前端的挂载路径，以 / 开头和结尾，例如 /admin/
  proxy: string[] // 转发到服务器的路径前缀，例如 ["/api/"]
}

/** 返回路径 pathname（URL 中的路径，未解码）的去向。 */
export function route(pathname: string, opts: RouteOptions): Route {
  const path = decodeURIComponent(pathname)
  if (opts.proxy.some((p) => path.startsWith(p))) return { kind: "proxy" }
  if (!path.startsWith(opts.base) && `${path}/` !== opts.base) return { kind: "missing" }
  const rel = path.slice(opts.base.length)
  const last = rel.slice(rel.lastIndexOf("/") + 1)
  const root = resolve(opts.root)
  const file = resolve(root, rel)
  if (file !== root && !file.startsWith(root + sep)) return { kind: "missing" }
  if (file !== root && existsSync(file) && statSync(file).isFile()) return { kind: "file", path: file }
  return last.includes(".") ? { kind: "missing" } : { kind: "shell" }
}
