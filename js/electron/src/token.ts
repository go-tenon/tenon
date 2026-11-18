// 宿主保存的登录 token：加密后写在一个文件中，页面拿不到。

import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { dirname } from "node:path"

/** 加密和解密 token，Electron 中是 safeStorage。 */
export interface Cipher {
  encrypt(plain: string): Uint8Array
  decrypt(data: Uint8Array): string
}

/** TokenStore 在文件 file 中保存加密后的 token，读过一次后缓存在内存中。 */
export class TokenStore {
  private cached: string | undefined
  private loaded = false

  constructor(
    private readonly file: string,
    private readonly cipher: Cipher,
    private readonly onUnreadable: (err: unknown) => void = () => {},
  ) {}

  /** 返回保存的 token；没有登录时返回 undefined。文件无法解密时（例如系统的钥匙串换了）删除它，调用 onUnreadable，当作没有登录。 */
  get(): string | undefined {
    if (this.loaded) return this.cached
    this.loaded = true
    if (!existsSync(this.file)) return undefined
    try {
      this.cached = this.cipher.decrypt(readFileSync(this.file))
    } catch (err) {
      rmSync(this.file, { force: true })
      this.onUnreadable(err)
    }
    return this.cached
  }

  /** 保存 token。 */
  set(token: string): void {
    mkdirSync(dirname(this.file), { recursive: true })
    writeFileSync(this.file, this.cipher.encrypt(token), { mode: 0o600 })
    this.cached = token
    this.loaded = true
  }

  /** 删除 token。 */
  clear(): void {
    rmSync(this.file, { force: true })
    this.cached = undefined
    this.loaded = true
  }
}
