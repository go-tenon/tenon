# 移动推送

把系统通知推送到手机上的 App（iOS、Android、HarmonyOS）：App 登录后登记设备，业务动作在事务中发送，worker 经[阿里云移动推送](https://help.aliyun.com/zh/document_detail/434629.html)送达。国内 Android 各厂商通道、APNs、FCM 的接入和密钥在阿里云控制台配置。

包：`push`、`push/aliyun`、`push/pushtest`。

- 设备由服务端登记在当前主体名下，同时记下这次登录的凭据；撤销凭据时一起删除设备，推送随之停止。
- `push.Send` 在业务事务中用 `tx` 发布推送任务：事务提交后才推送，回滚时一起取消。

<a id="example-usage"></a>
## 示例

```go
// App 登录后调用的接口中登记设备
err := push.Register(ctx, db, sub, push.Android, token)

// 业务事务中发送
err = push.Send(ctx, tx, push.Message{Title: "新文章", Body: item.Title}, recipients...)

// worker 中注册投递
provider, err := aliyun.New(cfg.Push, nil)
push.NewDeliverer(db, provider, push.Options{}).Handle(w)
```

`db` 是入口打开的 `*bun.DB`，`sub` 是当前主体，`token` 是推送 SDK 给出的 `deviceId`，`recipients` 是 `[]notification.Recipient`，`w` 是 `*queue.Worker`，`cfg.Push` 是配置中的 `push` 段。

<a id="installation"></a>
## 装配

1. 把 `queue.Migrations()` 和 `push.Migrations()` 加入[发布命令](../database/migrations.md#publishing-framework-migrations)，发布后执行迁移：推送任务走[队列](../background/queue.md)。
2. 把 `aliyun.Config` 放进根配置，字段标签为 `yaml:"push"`，见[配置](#aliyun-console)。
3. 在 worker 中注册 `Deliverer`，见[运行推送](#delivering)。
4. 把包级函数 `push.Erase`、`push.Export` 登记到[注销账号](privacy.md#framework-sources)和[导出个人数据](privacy.md#sources)；导出的设备不含 token。

<a id="aliyun-console"></a>
### 配置

在 EMAS 控制台为每个平台各建一个应用，填好各通道的密钥；RAM 用户需要 `mpush:PushV2` 权限。

```yaml
push:
  access_key_id: ${ALIYUN_ACCESS_KEY_ID}
  access_key_secret: ${ALIYUN_ACCESS_KEY_SECRET}
  app_keys: {android: 335000001, ios: 335000002} # 各平台应用的 AppKey；推送到没有 AppKey 的平台时任务失败
  apns: PRODUCT # 默认；App Store、TestFlight、企业分发用 PRODUCT，Xcode 直接安装的包用 DEV，不匹配时 iOS 收不到
  android:
    activity: com.example.blog.PushPopupActivity # 厂商通道的辅助弹窗 Activity，用厂商通道时必填
    channel: general # NotificationChannel ID
  categories:        # 见“消息分类”
    default: {huawei: SUBSCRIPTION, honor: 1, vivo: SUBSCRIPTION, oppo: SUBSCRIPTION, xiaomi: "135001", harmony: SUBSCRIPTION}
```

<a id="app-side"></a>
### App 端

App 按[阿里云的文档](https://help.aliyun.com/zh/document_detail/434660.html)集成推送 SDK 和厂商通道的依赖，把 SDK 的 `deviceId` 交给[登记接口](#registering-devices)，不在 SDK 中绑定账号。Android 8 及以上先创建 ID 为 `android.channel` 的 NotificationChannel，否则通知不显示；点击经厂商通道送达的通知时，系统打开 `android.activity`，在其中读出 `Data` 并跳转。

<a id="registering-devices"></a>
## 用 `Register` 登记设备

App 登录成功后、以及每次启动时调用 `PUT /devices`。`signedIn` 是应用中创建需要登录的操作的函数，`h.db` 是 `*bun.DB`：

```go
type DeviceInput struct {
    Body struct {
        Platform push.Platform `json:"platform" enum:"android,ios,harmony"`
        Token    string        `json:"token" minLength:"1" maxLength:"512" doc:"推送 SDK 的 deviceId"`
    }
}

rest.Register(api, signedIn("reader-device", http.MethodPut, "/devices"), h.registerDevice)

func (h *handlers) registerDevice(ctx context.Context, in *DeviceInput) (*struct{}, error) {
    sub, _ := identity.FromContext(ctx)
    return nil, push.Register(ctx, h.db, sub, in.Body.Platform, in.Body.Token)
}
```

- 一台设备同时只属于一个主体：同一部手机换账号登录后重新登记，旧账号不再收到。
- 一个用户的多台设备都收到推送。
- `Register` 记下 `sub.Tenant`：发给一个租户的推送只到达在这个租户中登记的设备。
- 平台不合法、token 为空或超过 512 字节时返回 `push.ErrInvalidDevice`（422）。

<a id="unregistering"></a>
### 退出登录时删除设备

撤销 token 时一起删除设备，与 [`apitoken` 的撤销方法](../security/api-tokens.md#revoking-tokens)一一对应：

| 撤销 token | 删除设备 | 用途 |
| --- | --- | --- |
| `Revoke(ctx, sub, id)` | `push.Unregister(ctx, db, sub, id)` | 退出登录（`id` 为 `sub.Credential`）、移除某台设备 |
| `RevokeOthers(ctx, sub)` | `push.UnregisterOthers(ctx, db, sub)` | 退出其他设备 |
| `RevokeSubject(ctx, ref)` | `push.UnregisterSubject(ctx, db, ref)` | 封禁账号、修改密码 |

漏掉删除时，退出登录的设备继续收到推送，直到被[清理](#cleanup)或被别的账号重新登记。

<a id="sending"></a>
## 用 `Send` 发送推送

在[业务动作](../concepts/actions.md#writing-actions)的事务中调用。`a.db` 是 `*bun.DB`，`subscribers` 是应用的查询，返回 `[]identity.Ref`：

```go
err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
    // ……读取、检查并更新文章
    readers, err := subscribers(ctx, tx, item.Section)
    if err != nil {
        return err
    }
    to := make([]notification.Recipient, len(readers))
    for i, r := range readers {
        to[i] = notification.Recipient{Ref: r}
    }
    return push.Send(ctx, tx, push.Message{
        Title: "新文章",
        Body:  item.Title,
        Data:  map[string]string{"article_id": item.ID.String()},
    }, to...)
})
```

- 收件人与[站内通知](notifications.md#recipients)的相同，同一组收件人可以同时交给 `notification.Send`。
- 设备在任务执行时才查询，没有登记设备的收件人直接跳过。重复的收件人只推送一次，超过 1000 个时分成多个任务。

<a id="messages"></a>
### 消息

| `push.Message` 的字段 | 说明 |
| --- | --- |
| `Title`、`Body` | 必填。文案由应用按收件人的语言生成 |
| `Data` | 带给 App 的键值，点击后 App 据此打开页面 |
| `Category` | [消息分类](#categories)，为空时是 `default` |
| `Badge` | 图标上的数字，`nil` 保持原值；iOS 和鸿蒙可以设为 0 清除，Android 只在华为、荣耀、vivo 上生效（1 到 99） |
| `TTL` | 设备离线时保留多久，从 `Send` 算起，最长 72 小时；队列积压超过 TTL 的推送直接丢弃 |

> [!WARNING]
> 锁屏界面上任何人都看得到通知。私密内容写成“你有一条新消息”，App 打开后从接口读取。

<a id="categories"></a>
### 消息分类

国内厂商把通知分成服务与通讯、资讯营销两类；资讯营销每台设备每天只能收到几条并静默展示，没有分类参数的消息通常按它处理。`Message.Category` 是应用起的名字，`categories` 把它换成各家的参数（大多要先在厂商后台申请）：

```yaml
push:
  categories:
    promo: {huawei: MARKETING, honor: 0, vivo: MARKETING, oppo: MARKETING, harmony: MARKETING, xiaomi: "135002", channel: promo}
```

| 键 | 厂商参数 |
| --- | --- |
| `huawei`、`vivo`、`oppo`、`harmony` | 各家的 `category`，营销消息为 `MARKETING` |
| `honor` | 荣耀的 `importance`：1 为服务与通讯，0 为资讯营销 |
| `xiaomi` | 小米推送后台申请的 channel ID，每个应用最多 8 个 |
| `channel` | 覆盖 `android.channel`，让用户可以只关闭这一类通知 |

指定了配置中没有的分类时，推送任务直接失败，原因记在[失败任务](../background/queue.md#failed-jobs)中。

<a id="delivering"></a>
## 运行推送

在 [worker](../background/queue.md#processing-jobs) 中注册。`cfg.Push` 是 `aliyun.Config`，`w` 是 `*queue.Worker`：

```go
provider, err := aliyun.New(cfg.Push, nil) // 不访问网络；客户端为 nil 时用 outbound 的客户端
if err != nil {
    return err
}
push.NewDeliverer(db, provider, push.Options{}).Handle(w)
```

处理器按平台把设备分批（每批最多 1000 台）调用阿里云的 PushV2。阿里云接受请求即算成功；送达和点击在控制台的回执日志中查看，`job_key` 是推送任务的 ID。

<a id="retries"></a>
### 重试与去重

- 限流、服务端错误和网络错误按队列的退避重试，默认最多 5 次（约 6 分钟），用 `push.Options.MaxAttempts` 修改。
- AccessKey、AppKey、参数等 4xx 错误不重试，任务直接失败。
- 一批失败时整个任务重试；已成功的批次带相同的 `IdempotentToken` 再调用，阿里云 15 分钟内只推送一次。

<a id="overseas"></a>
## 海外用户

同一个应用和配置：iOS 经 APNs，有 Google 服务的 Android 经 FCM（控制台配置 FCM 通道，App 集成阿里云的 FCM 依赖），其他 Android 只在 App 运行时收到。

<a id="testing"></a>
## 测试

只检查发布了哪些推送时，用 `queuetest.Pending` 读出未处理的任务，`db` 是测试库：

```go
jobs := queuetest.Pending(t, db, push.Deliver)
if len(jobs) != 1 || jobs[0].Message.Title != "新文章" {
    t.Fatalf("jobs %+v", jobs)
}
```

端到端测试时把 `pushtest.Provider` 交给 `push.NewDeliverer` 代替阿里云：`Batches`、`Tokens` 返回收到的推送，设置 `Err` 模拟失败。测试 `push/aliyun` 时把 [`outboundtest`](../libraries/outbound.md#testing) 的客户端传给 `aliyun.New`。

<a id="cleanup"></a>
## 用 `Cleanup` 删除不活跃的设备

App 每次启动都重新登记，长期没有更新的设备通常已卸载 App。`sched` 是 `*schedule.Scheduler`，交给[定时任务](../background/scheduling.md#framework-cleanups)：

```go
sched.Add("push:cleanup", "@daily", schedule.Cleanup(push.Cleanup(a.db, 90*24*time.Hour), 1000))
```
