---
name: tenon-electron
description: Tenon 应用的 Electron 桌面端（clients/<名称>）：生成与运行、在系统浏览器中登录、在 main.ts 中组合托盘、通知、开机自启等桌面能力、页面调用宿主、打包签名与自动更新。修改桌面端或者给它加桌面能力时使用。
---

# Electron 桌面端

桌面端在窗口中显示 `apps/` 中的一个前端，由 `@go-tenon/electron` 实现宿主。先读 `tenon/docs/clients/electron.md` 和 `tenon/docs/clients/overview.md`。

## 步骤

1. **生成**：`go tool tenon add client <名称> --kind electron --frontend <前端>`，前端要有登录页。它生成 `clients/<名称>`，并在前端中加上授权页，见 `tenon/docs/clients/electron.md#minimal-example`。
2. **登记客户端**：服务端用 OAuth 授权服务器登记它，ID 与目录名相同，见 `tenon/docs/clients/native.md#registering`。
3. **运行**：`go tool tenon dev --client <名称>`。修改前端后窗口中的页面立即更新；修改 `src` 中的主进程代码后关掉窗口重新运行。
4. **桌面能力**：在 `src/main.ts` 中逐行组合 `windowState`、`standardMenu`、`singleInstance`、`tray`、`notifications`、`badge`、`autostart`、`deepLinks` 等函数，见 `tenon/docs/clients/electron.md#capabilities`。
5. **页面调用宿主**：页面经 `@go-tenon/host` 的 `host()` 取得 `tenonHost`，见 `tenon/docs/clients/electron.md#from-the-page`、`tenon/docs/clients/host.md`。
6. **原生方法**：需要宿主没有的能力时自己加，见 `tenon/docs/clients/electron.md#native-methods`。
7. **打包与发布**：`go tool tenon build --client <名称>`；签名、CI 中为三个平台打包、自动更新和强制升级见 `tenon/docs/clients/electron.md#packaging`。

## 要点

- 登录在系统浏览器中完成（授权码 + PKCE），桌面端保存 token，用 `Authorization: Bearer` 调用接口，见 `tenon/docs/clients/electron.md#sign-in`。
- 依赖 Electron 的代码只写在 `main.ts`、`preload.ts` 中。
- 上线前把 `src/main.ts` 中服务器的地址改成线上的地址，图标换成自己的（`assets/tray.png`、`build/icon.png`）。
- 桌面端每个请求都带 `X-App-Version`，服务端可以要求最低版本，见 `tenon/docs/http/clients.md#app-version`。
