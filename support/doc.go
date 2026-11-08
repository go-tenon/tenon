// Package support 收录各产品通用、与业务无关的工具函数。
//
// # 分工
//
// 社区已有成熟实现的，产品直接依赖那个库，本目录不做二次包装：
//
//	切片/map 函数、指针取值、命名风格转换   github.com/samber/lo
//	                                        （lo.Map、lo.GroupBy、lo.KeyBy、lo.UniqBy、
//	                                         lo.FromPtrOr、lo.SnakeCase、lo.Ellipsis …）
//	重试与退避                              github.com/cenkalti/backoff/v5
//	并发编排、请求合并、信号量              golang.org/x/sync（errgroup、singleflight、semaphore）
//	限流                                    golang.org/x/time/rate
//	金额等精确小数                          github.com/shopspring/decimal
//	UUID                                    标准库 uuid（主键用 uuid.NewV7()）
//
// 本目录只放社区库缺少、或现有实现不适合生产环境直接使用的部分：
//
//	support          Catch：把 panic 转成带调用栈的 error（lo.TryCatch 会丢弃错误与调用栈）
//	support/crypt    用一个密钥做 AES-256-GCM 加密与解密（crypto/cipher 过于底层，易用错）
//	support/password 基于 argon2id 的密码哈希与校验（x/crypto/argon2 只提供底层 KDF）
//	support/random   密码学安全的 token、验证码（lo.RandomString 基于非安全随机数）
//	support/strx     手机号、身份证、邮箱脱敏
//	support/set      泛型集合（lo 只有基于切片的集合运算）
//
// # 约定
//
//   - 各子包彼此独立；不依赖本仓库的 config、logger、errs。
//   - 普通输入（空值、nil、越界长度）不会 panic，也不修改入参。
//   - 字符串按字符（rune）计数，而不是按字节。
package support
