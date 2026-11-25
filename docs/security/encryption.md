# 哈希、加密与签名链接

`hashing` 为密码和口令生成哈希并校验；`crypt` 用应用密钥加密以后还要读出明文的值（第三方 API Key、refresh token），并给链接签名。

包：`hashing`、`crypt`。

- 密码和口令用 `hashing`，不加密：哈希无法还原，数据库泄露时也拿不到明文。
- `Hasher` 和 `Crypt` 都在[入口](../concepts/dependencies.md#assembling-the-application)中创建一次，可以并发使用，传给需要它的业务动作和处理器。

<a id="example-usage"></a>
## 示例

`cfg` 是应用的根配置，含 `hashing` 段和 `crypt` 段：

```go
hasher := hashing.New(cfg.Hashing)
hash, err := hasher.Make(passcode) // 保存 hash
ok := hasher.Check(input, hash)    // 校验

c := crypt.New(cfg.Crypt)
secret := c.Encrypt(apiKey)       // 保存密文
apiKey, err = c.Decrypt(secret)   // 用到时解密

link := c.SignURL("https://example.com/email/verify/42", 24*time.Hour)
err = c.VerifyURL(r.URL) // 签名不对返回 crypt.ErrInvalidSignature，过期返回 crypt.ErrExpired
```

<a id="hashing-passwords"></a>
## 密码哈希：`Hasher.Make`、`Hasher.Check`

账号的密码由[登录与注册](login.md)保存和校验，把 `Hasher` 交给 [`login.New`](login.md#assembling) 即可。应用自己保存要校验的口令时直接用 `Hasher`，例如文章的访问口令。`a.hasher` 是入口创建的 `*hashing.Hasher`，`a.db` 是 `*bun.DB`：

```go
hash, err := a.hasher.Make(in.Passcode)
if err != nil {
    return err
}
_, err = a.db.NewUpdate().Model((*Article)(nil)).Set("passcode_hash = ?", hash).Where("id = ?", id).Exec(ctx)
```

```go
if !a.hasher.Check(in.Passcode, art.PasscodeHash) {
    return nil, ErrWrongPasscode.New()
}
```

- 哈希是 PHC 格式的字符串，例如 `$argon2id$v=19$m=19456,t=2,p=1$<盐>$<哈希>`，算法、参数和随机盐都在其中，存在一个文本列中。格式与 PHP 的 `password_hash` 互通。
- `Make` 每次用新的随机盐，同一个口令两次的结果不同。`Check` 用常量时间比较。
- 口令可以被反复猜测，校验接口要[限流](../libraries/rate-limiting.md#in-handlers)。
- `driver` 为 bcrypt 时，超过 72 字节的密码 `Make` 返回 `hashing.ErrPasswordTooLong`，在输入上用 `maxLength:"72"` 限制。argon2id 没有这个限制。

<a id="rehashing"></a>
## 重新哈希：`NeedsRehash`

`Check` 按哈希中记录的算法校验 argon2id 和 bcrypt，与当前的 `driver` 无关，所以调高参数、切换算法或导入旧系统的哈希之后，已有的哈希仍然可以校验。`NeedsRehash` 在哈希的算法或参数与当前配置不同时为真；校验通过时手上有明文，在这时换成新的哈希：

```go
if a.hasher.NeedsRehash(art.PasscodeHash) {
    hash, err := a.hasher.Make(in.Passcode)
    if err != nil {
        return nil, err
    }
    _, err = a.db.NewUpdate().Model((*Article)(nil)).Set("passcode_hash = ?", hash).
        Where("id = ? AND passcode_hash = ?", art.ID, art.PasscodeHash).Exec(ctx)
    if err != nil {
        return nil, err
    }
}
```

- 条件中带上旧的哈希：校验期间口令可能刚被修改，无条件写入会把旧口令写回去。
- `login` 在密码登录成功时同样这样做，用户下次登录时迁移到新的算法和参数。从其他系统导入账号时，把旧的 bcrypt 或 argon2id 哈希原样写入 `auth_passwords` 表。

<a id="hashing-configuration"></a>
## 哈希的配置

`hashing.Config` 放进根配置结构体的 `hashing` 段。[`config.Load`](../general/configuration.md#defaults-and-validation) 填充默认值并校验，`driver` 写错或参数不合法时启动失败。通常保持默认：

```yaml
hashing:
  driver: argon2id     # argon2id 或 bcrypt
  argon2id:
    memory: 19456      # 每次哈希使用的内存，单位 KiB
    time: 2            # 迭代次数
    threads: 1         # 并行度
  bcrypt:
    cost: 12           # 每加 1，耗时翻倍
```

- argon2id 的默认值是 OWASP 推荐的最低配置。参数越大越难破解，登录也越慢、越占内存；在生产机器上把单次哈希控制在几十到几百毫秒。
- `hashing.New` 给没有设置的项（零值的 `Driver`、整组 `Argon2id`、`Bcrypt.Cost`）补上默认值，补完仍然无效时 panic。测试中只写要改的项，例如 `hashing.Config{Driver: "bcrypt", Bcrypt: hashing.BcryptConfig{Cost: 4}}`。

<a id="configuration"></a>
## 应用密钥的配置

`crypt.Config` 放进根配置结构体的 `crypt` 段：

```go
type Config struct {
    Crypt crypt.Config `yaml:"crypt"`
}
```

```yaml
crypt:
  key: ${APP_KEY}        # 32 字节随机数的 base64：openssl rand -base64 32
  previous_keys: []      # 轮换前的旧密钥，只用于解密和验签，见“轮换密钥”
```

- `config.Load` 在启动时校验：`key` 缺失、不是 base64 或长度不是 32 字节时启动失败。
- 加密和签名的子密钥由应用密钥用 HKDF 派生，互不相同。

> [!WARNING]
> 应用密钥和数据库一起妥善保管。密钥丢失或换了环境，已加密的值无法解开，已发出的签名链接全部失效。

<a id="encrypting-data"></a>
## 加密数据：`Encrypt`、`Decrypt`

`Encrypt` 返回 URL 安全的 base64 文本，存在普通的文本列中；每次加密使用新的随机数，同一个值两次的结果不同。`a.crypt` 是入口创建的 `*crypt.Crypt`：

```go
p := &Provider{ID: id, TenantID: sub.Tenant, BaseURL: in.BaseURL, APIKey: a.crypt.Encrypt(in.APIKey)}
if _, err := a.db.NewInsert().Model(p).Exec(ctx); err != nil {
    return nil, errors.Wrap(err, "create provider")
}
```

```go
key, err := a.crypt.Decrypt(p.APIKey)
if err != nil {
    return errors.Wrapf(err, "decrypt api key of provider %s", p.ID)
}
```

`Decrypt` 依次尝试 `key` 和 `previous_keys`。密文被改过，或者加密它的密钥已经不在配置中时返回 `crypt.ErrDecrypt`，接口返回 500 并记录日志。

<a id="signed-urls"></a>
## 签名链接：`SignURL`

邮件中的链接（验证邮箱、退订）用 `SignURL` 加上 `expires` 和 `signature` 两个查询参数，`ttl` 后过期。基址从 [`http.public_url`](../http/middleware.md#public-url)（`cfg.HTTP.BaseURL()`）传入业务动作 `a.publicURL`：

```go
link := a.crypt.SignURL(a.publicURL+"/email/verify/"+u.ID+"?email="+url.QueryEscape(u.Email), 24*time.Hour)
// https://example.com/email/verify/42?email=a%40b.com&expires=1731400000&signature=Xy...
```

- 签名覆盖路径和其余全部查询参数，改了任何一处签名就对不上；路径区分了链接的用途。协议和域名不在签名内，经过反向代理后处理器看到的内网地址照样能校验。
- 签名只防篡改，链接中的参数谁都能看到。

> [!WARNING]
> 有的邮件服务商改写链接，追加 `utm_source` 等统计参数，签名随之失效。发送这类邮件时在服务商处关闭链接追踪。

<a id="validating-signed-urls"></a>
### 校验链接：`VerifyURL`

链接从邮件打开，任何人都能访问，所以指向公开的页面或接口。处理器把 `r.URL` 交给 `VerifyURL`；单页应用中，前端把整个地址交给 API，API 用 `url.Parse` 解析后交给它。`h.crypt` 是 `*crypt.Crypt`，`h.verifyEmail` 是业务动作：

```go
if err := h.crypt.VerifyURL(r.URL); err != nil {
    return err // 签名不对：crypt.ErrInvalidSignature；过期：crypt.ErrExpired
}
return h.verifyEmail.Handle(r.Context(), chi.URLParam(r, "id"), r.URL.Query().Get("email"))
```

- 两种错误都是 `errorx.PermissionDenied`，返回 403。过期页可以用 `errors.Is(err, crypt.ErrExpired)` 区分，提供重新发送的入口。
- `VerifyURL` 只证明链接是本站发出的，业务动作仍按当前数据判断：`verifyEmail` 要确认用户当前的邮箱仍是链接中的 `email`，用户在此期间改过邮箱，旧链接就失效。

<a id="permanent-urls"></a>
### 永不过期的链接

`ttl` 为 0 时不加 `expires`，链接不过期，用于退订这类在旧邮件中也要能用的链接：

```go
link := a.crypt.SignURL("https://example.com/unsubscribe/"+u.ID, 0)
```

一次性链接（例如重置密码）将令牌保存在数据库中，使用后删除。

<a id="rotating-keys"></a>
## 轮换密钥

把新密钥放进 `key`，旧密钥移到 `previous_keys`：

```yaml
crypt:
  key: ${APP_KEY}
  previous_keys:
    - ${APP_PREVIOUS_KEY}
```

- 新加密的数据和新发出的链接使用新密钥；`Decrypt`、`VerifyURL` 依次尝试 `key` 和 `previous_keys`，旧数据和旧链接仍然有效。
- 旧密钥保留到用它加密的数据都换成新密钥为止：写一个一次性的[命令](../general/commands.md#custom-commands)逐行 `Decrypt` 再 `Encrypt`。密钥泄露时，换完数据后从 `previous_keys` 中删除旧密钥，旧链接随之失效。
- 其他组件需要自己的密钥时调用 `Derive(purpose)`，例如 `login` 签名验证码。它返回从 `key` 和 `previous_keys` 派生的子密钥：第一个用于签名，全部用于校验，所以随应用密钥一起轮换。`purpose` 包含包名和版本，例如 `"tenon login code v1"`。

<a id="testing"></a>
## 测试

`crypt.GenerateKey()` 生成测试用的密钥；链接过期用 [testing/synctest](../testing/testing.md#controlling-time) 推进时间：

```go
c := crypt.New(crypt.Config{Key: crypt.GenerateKey()})

synctest.Test(t, func(t *testing.T) {
    link, _ := url.Parse(c.SignURL("https://example.com/email/verify/42", time.Hour))
    time.Sleep(time.Hour)
    assert.ErrorIs(t, c.VerifyURL(link), crypt.ErrExpired)
})
```

`hashing.New(hashing.Config{})` 使用默认配置；测试中用 bcrypt、`cost` 为 4 可以加快速度。
