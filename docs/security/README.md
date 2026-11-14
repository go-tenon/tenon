# 安全

认证、登录、授权与密码学工具。

建议先读完整示例，再按需查专题章：

1. [单账号后台](admin-example.md)：邮箱密码 + JWT cookie + 保护路由
2. 各专题章拆开讲机制
3. [运营后台与多租户](saas-example.md)：两套账号、租户、代登录

动手改博客时仍跟[入门教程：作者登录](../tutorial/06-login.md)。

- [单账号后台](admin-example.md)：数据库驱动的后台登录认证最短闭环
- [认证](authentication.md)：识别请求的主体，Bearer 令牌
- [登录与注册](login.md)：邮箱、密码、验证码、Passkey 与二次验证
- [API 令牌](api-tokens.md)：不透明令牌与 JWT
- [第三方登录](social-login.md)：OAuth 与 OpenID Connect 登录
- [企业单点登录](enterprise-sso.md)：对接企业身份提供方
- [OAuth 授权服务器](oauth-server.md)：让 MCP 客户端等第三方获得授权
- [运营后台与多租户](saas-example.md)：运营人员、租户成员与代登录
- [多租户](tenancy.md)：单数据库多租户
- [授权](authorization.md)：角色、权限与租户范围
- [哈希](hashing.md)：密码哈希
- [加密与签名链接](encryption.md)：应用密钥、加解密、签名链接
