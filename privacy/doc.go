// Package privacy 删除注销账号的个人数据：用户申请注销后经过冷静期，再一次删除框架和应用保存的这个主体的全部数据。
//
// 一个服务注销一套账号体系（例如 user）的主体，与 login.Realm 一一对应。应用在装配时用 Source 列出保存了主体数据的地方。每一项的 Erase 在同一个事务中执行，签名与框架各包的
// Erase 函数相同，可以直接登记；Check 决定能不能注销，例如“你是团队唯一的所有者，请先转让”：
//
//	deletions := privacy.New(db, privacy.Options{
//	    Type:  user.Type,
//	    Grace: 15 * 24 * time.Hour,
//	    Sources: []privacy.Source{
//	        {Name: "user", Check: user.CanErase, Erase: user.Erase},
//	        {Name: "login", Erase: realm.Forget},
//	        {Name: "api_tokens", Erase: apitoken.Erase},
//	        {Name: "push", Erase: push.Erase},
//	        {Name: "notifications", Erase: notification.Erase},
//	    },
//	})
//
// Request 在业务事务中申请注销，冷静期内 Pending 查询、Cancel 撤销；EraseDue 删除冷静期已过的主体，交给定时任务：
//
//	sched.Add("privacy:erase", "*/10 * * * *", schedule.Cleanup(deletions.EraseDue, 100))
//
// Erase 跳过冷静期立即删除，例如管理员删除账号。删除完成后 privacy_erasures 中保留一行，只记录主体的类型、ID 和时间。
package privacy
