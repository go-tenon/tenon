# examples/api

手写参考 HTTP API：配置、命令、订单业务、Bearer 演示鉴权。

## 快速开始

```bash
export DATABASE_URL='postgres://postgres:postgres@127.0.0.1:55432/orders?sslmode=disable'
createdb orders   # 或使用任意空库
go run . migrate up --config config.yaml
go run . serve --config config.yaml
```

离线 OpenAPI（不读运行密钥、不打开数据库）：

```bash
go run . openapi
```

演示 token：`Authorization: Bearer dev`（用户 alice）。

开发命令见 `Taskfile.yml`。
