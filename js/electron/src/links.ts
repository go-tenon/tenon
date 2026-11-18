// 深度链接的纯逻辑：<scheme>://open/<路径> 中的路径与网页上的相同，只接受前端挂载路径之下的页面。

/** 返回深度链接 url 要打开的页面路径（带查询参数），例如 admin-desktop://open/admin/orders/123 得到 /admin/orders/123。
 * 协议不是 scheme、不是 open、路径不在 base 之下时返回 undefined。 */
export function linkPath(url: string, scheme: string, base: string): string | undefined {
  let u: URL
  try {
    u = new URL(url)
  } catch {
    return undefined
  }
  if (u.protocol !== `${scheme}:` || u.host !== "open") return undefined
  const path = u.pathname
  if (path.startsWith("//") || (!path.startsWith(base) && `${path}/` !== base)) return undefined
  return path + u.search
}

/** 返回命令行参数 argv 中第一个属于 scheme 的深度链接（Windows、Linux 把链接作为参数交给新启动的进程）。 */
export function linkInArgv(argv: string[], scheme: string): string | undefined {
  return argv.find((a) => a.startsWith(`${scheme}://`))
}

/** 返回 Linux 开机自启的 .desktop 文件内容（XDG Autostart）。exec 是可执行文件，AppImage 时是 AppImage 文件的路径。 */
export function autostartEntry(name: string, exec: string): string {
  const quoted = `"${exec.replace(/(["`$\\])/g, "\\$1")}"`
  return `[Desktop Entry]\nType=Application\nName=${name}\nExec=${quoted}\nX-GNOME-Autostart-enabled=true\n`
}
