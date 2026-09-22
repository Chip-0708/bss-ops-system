# Swagger UI 本地联调说明（给前端同事）

后端自带在线接口文档，浏览器里可以直接调接口，不用先造前端页面。

看完这篇你能做到：本地起服务 → 打开接口文档页 → 登录拿到 token → 在网页上直接发请求看结果。

---

## 0. 先拉最新代码（重要）

我现在 GitHub 上的 master 比本地落后一批提交，你 clone 下来的可能缺成本模块（6a worker / 6b 成本基线 / Swagger UI）。

先让我推一次，你再执行：

```
git pull
```

判断有没有拉全：项目里能看到 `internal/api/cost.go`、`migrations/000016_cost_param.up.sql`、`internal/api/docs.go` 三个文件，就是新的。

---

## 1. 环境准备

1. Go（建议 1.22 以上），命令行 `go version` 有输出即可
2. Docker Desktop，且能正常启动容器
3. golang-migrate 命令行工具（`make migrate-up` 要用）
   - 装法：`go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest`
   - 装完确认 `$GOPATH/bin`（一般是 `~/go/bin`）在 PATH 里，`migrate -version` 有输出

---

## 2. 起数据库

项目自带 PG 容器，**端口是 5433，不是 5432**（避免和机器上已有的 PostgreSQL 冲突）：

```
make pg-start
```

首次会创建一个容器 `model_bss_pg`，库名 `model_bss`，用户 `app`，密码 `dev_only`。

容器起没起：`docker ps` 里能看到 `model_bss_pg` 且状态 Up。

---

## 3. 建表

```
make migrate-up
make migrate-version
```

版本应该是 19 或更大。这一步会把建表和种子数据（厂商、模型、供应商、账号）全部建好。

如果报数据库连接失败，先确认第 2 步容器活着，再看第 4 步的配置。

---

## 4. 配置文件（这一步容易漏）

仓库里只有模板，**真实配置被 gitignore 了，每人本地一份**：

```
cp configs/config.example.yaml configs/config.yaml
```

然后打开 `configs/config.yaml`，把数据库密码填上：

```
db:
  host: 127.0.0.1
  port: 5433
  user: app
  password: "dev_only"     # ← 模板里是空的，必须改
  dbname: model_bss
```

其余保持不动。这个配置里的 Swagger 开关默认是开的：

```
docs:
  enable: true
```

---

## 5. 起服务

```
go run ./cmd/server
```

看到监听 8080 的日志就成了。另开一个终端验证：

```
curl http://127.0.0.1:8080/healthz
```

返回里有 `"code":0` 就是正常。

---

## 6. 打开接口文档页

浏览器打开：

```
http://127.0.0.1:8080/swagger/index.html
```

（`http://127.0.0.1:8080/swagger` 也行，会 301 跳过去）

页面里按业务分组（模型管理、报价、成本管理等）列出所有接口，每个都能展开看参数、点 Try it out 直接发请求。

另外两个原始地址，导到 Postman / Apifox 用：

- `http://127.0.0.1:8080/openapi/swagger.json`
- `http://127.0.0.1:8080/openapi/swagger.yaml`

Apifox 里「导入 → URL 或文本」，填 json 那个地址即可。

---

## 7. 登录拿 token

绝大多数接口都要登录才能调。

在文档页找到 **成本管理** 或 **认证** 分组里的：

```
POST /api/internal/auth/login
```

点 Try it out，请求体改成：

```
{"login_id":"smoke_admin","password":"Test@1234"}
```

点 Execute，响应里：

```
{"code":0,"data":{"token":"06aabd1d...（64 位十六进制）", ...}}
```

把 `data.token` 那一长串复制出来。

---

## 8. 鉴权（最容易踩的一步）

点页面右上角的 **Authorize** 按钮，弹窗里粘贴：

```
Bearer 06aabd1d...（你刚复制的 token）
```

**注意三点：**

1. 必须带 `Bearer ` 前缀和一个空格，只粘 token 会 401
2. 本项目导出的是 OpenAPI 2.0，安全方案是 `apiKey(in: header, name: Authorization)`，Swagger UI 不会自动帮你加 `Bearer`，所以要手动连前缀一起粘
3. 点 Authorize 关掉弹窗后，后续所有 Try it out 会自动带上这个头

**token 有效期 12 小时**（滑动续期）。过了就重新登一次、重新 Authorize。调接口返回 `code:10002 未认证` 就是这个原因。

---

## 9. 可以直接用的账号

密码统一 `Test@1234`。

- `smoke_admin`：超管 + 模型运营 + 定价运营，数据域 ALL，M1~M12 权限点全开。**联调默认用这个**，什么接口都能调
- `buyer_a` / `buyer_b`：采购角色（PROCUREMENT）
- `smoke_sales`：销售角色（SALES）
- `smoke_bursty`：定价运营，专门用来测登录限流的，联调别用（连续输错会被锁 15 分钟）
- `supplier_a` / `supplier_b`：供应商账号，走 `POST /api/supplier/auth/login`，看到的是 `/api/supplier/*` 那一组接口

内部账号不能登供应商门户，反过也一样——这是按门户隔离的。

---

## 10. 两个能立刻跑通的例子

### 例一：成本基线列表

```
GET /api/internal/cost/baselines?page=1&size=20
```

带 admin 的 token，返回（节选）：

```
{"code":0,"data":{"list":[{"sku_id":40,"sku_code":"gpt-5-2026-04-11","version":3,
"primary_supplier_id":1,"unit_cost":"2.60075000","floor_price":"3.05970588",
"supplier_count":1,"single_point":true}],"total":2,"page":1,"size":20}}
```

还支持 `keyword`（sku_code 模糊匹配）和 `only_single_point=true`（只看独家报价的 SKU）。

### 例二：某个 SKU 的成本版本历史

```
GET /api/internal/cost/baselines/40/history
```

`40` 可以填 `sku_id`（纯数字）也可以填 `sku_code`。返回里能看到 `calc_snapshot`（计算快照：用到的损耗率/通道费、各家供应商的四因子评分）。

带 `asOf` 参数可以查历史某个时刻生效的版本：`?asOf=2026-09-01T00:00:00+08:00`，查不到返回空 list 而不是报错。

### 用 `smoke_sales` 登一次，看看字段剔除

同一个列表接口，admin 看到 11 个字段，sales 只看到 9 个——`unit_cost`、`unit_cost_basis` 被服务端物理删掉了，但 `floor_price` 保留（销售必须能看到售价下限）。

这不是 bug，是按角色的字段剔除（field_mask）。前端如果发现某个字段时有时无，先换 admin 确认字段本身存在，再查是不是当前角色看不到。

---

## 11. 响应结构约定

所有接口（含报错）统一是这层壳：

```
{"code":0,"message":"ok","data":{...},"requestId":"..."}
```

前端判断逻辑：**先看 HTTP 状态码，再看 code**。`code != 0` 时不要读 `data`（一定是 null）。`message` 是能直接展示给用户的中文。

常用错误码：

- `10001` 参数不合法
- `10002` 未认证（没带 token 或 token 过期）
- `10003` 无权限（该账号没有这个权限点）
- `10004` 资源不存在
- `409` 类业务冲突（比如非终态互斥）

排查问题时把 `requestId` 发我，能直接定位到日志。详见 `docs/api/README.md`。

---

## 12. 联调常见坑

1. **401 / 10002** —— 忘了点 Authorize，或者粘贴时丢了 `Bearer ` 前缀，或者 token 过期了
2. **403 / 10003** —— 当前账号没有这个接口的权限点。先用 `smoke_admin` 确认接口本身是通的
3. **字段缺失** —— 见第 10 节末尾，是角色字段剔除，不是接口坏了
4. **分页参数是 `page` 和 `size`**，不是 `page_size`、`pageSize`。`size` 上限 100
5. **金额全是字符串**，比如 `"2.60075000"`、`"3.05970588"`。前端不要用 JS number 直接参与计算，会丢精度；展示用字符串，计算要引 decimal 库或交给我后端算
6. **时间全是 RFC3339 字符串**，带时区（服务端存 UTC，返回带 `Z`），前端按本地时区格式化即可
7. **写接口要带幂等键**：POST 类接口（新建模型、提交报价、审批、导入确认等）需要请求头 `Idempotency-Key`，值是客户端生成的唯一串。重复请求同一个 key 会返回首次结果，重复提交不会造脏数据。**哪些接口必填，文档页里该接口展开后能看到这个头标了 required**
8. **接口文档更新了怎么办** —— 我改了接口注解后会执行 `make swag` 重新生成 `openapi/swagger.json`（这个是产物，别手改）。你那边重启服务就是最新的

---

## 13. 生产环境

那个 Swagger 页面会列出全部接口和参数，属内网调试资产。生产部署时配 `docs.enable: false`（或环境变量 `MODEL_BSS_DOCS_ENABLE=false`），页面和 `/openapi/*` 就都不注册了。

---

有报错先把响应里的 `requestId` 和 `code` 发我。
