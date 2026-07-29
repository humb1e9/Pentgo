# Recon（入口与资产发现）

以**已回灌的执行结果**为依据，逐步减少目标未知信息。需要操作时生成 Python 或 Bash 代码块；每个块必须打印可供下一轮判断的实际输出（状态码、关键头、标题、表单字段、链接、错误信息）。

优先使用环境变量 `PENTGO_TARGET`、`PENTGO_WORKDIR` 与工作目录中已有文件。不要假设固定工具链或固定扫描阶段；根据当前证据选择下一步。

只有 stdout、stderr、退出码和 `[evidence_ref: N]` 构成运行时证据。任务意图已被证据覆盖时，在不带工具调用的普通助手回复中给出简短结论。

---

## 1. 范围纪律（硬约束，先于一切扩展）

1. **默认同主机**：只请求 `PENTGO_TARGET` 解析出的主机（及该主机上的路径/子路径）。  
2. **跨主机 / 子域**：仅当操作者 intent 或运行配置明确允许（例如写明 `*.example.com`、附加 allowed host）。未允许则**禁止**子域爆破、旁站扫描、证书透明度拉全网。  
3. 运行时会拦截越权主机与破坏性写操作；被拦截说明你越界了——收窄目标，不要换更猛的绕过去打站外。  
4. **非破坏、低速率**：路径探测用小词表；请求间隔（建议 ≥1s 对真实站）；不密码爆破；不上传/删除/改生产数据；一次失败登录探测即可，禁止字典撞库。

---

## 2. 建议优先序（指导，不是强制 phase）

在宽泛评估或表面未知时，按证据推进，可跳过已有结论的步骤：

| 顺序 | 动作 | 产出证据 |
|------|------|----------|
| A | 请求目标根 URL：状态码、最终 URL、标题、关键响应头 | 指纹与是否存活 |
| B | 解析 HTML：`a[href]`、`form`、`script src`、注释中的路径 | 同站链接与表单 |
| C | 轻量探测常见入口（仅同 host，小列表） | 401/302/表单/404 分布 |
| D | 识别**认证面**（见 §3） | login URL、字段名、会话线索 |
| E | 打印 **ASSET MAP**（§5） | 可回灌的资产摘要 |
| F | 有登录面 → `SKILL_LOAD` 认证/越权类 skill，再做有限探测 | 为框架 `login_url` 提供证据 |
| G | 无登录面 → 公开面反射/注入等，**不要**编造 credential | 公开面 finding 证据 |

不要在未记录任何入口证据时声称「无攻击面」或「已完全 recon」。

---

## 3. 入口与认证信号

**HTML / 前端**

- `input[type=password]`、用户名/邮箱字段、`login` / `signin` / `sso` / `oauth` / `passport` 文案  
- `form[action]` 指向登录或 token 端点  
- 前端路由或配置 JSON 中的 `apiBase`、`authUrl`

**HTTP**

- `401` / `403`、`WWW-Authenticate`  
- `302`/`301` 的 `Location` 含 `login` / `sso` / `cas` / `oauth`  
- `Set-Cookie` 会话名（记录**名字**即可，不要在报告里当密钥传播）

**API / 管理**

- `/api`、`/graphql`、`/v1`、`/swagger`、`/openapi`、`/admin`、`/manage`、`/console`  
- JSON 错误体中的 `unauthorized` / `unauthenticated`

**常见同 host 轻量路径（示例，可裁剪，禁止海量字典）**

`/login` `/signin` `/user/login` `/admin` `/admin/login` `/api` `/api/v1` `/oauth` `/sso` `/cas` `/account` `/passport`

每条路径记录：状态码、是否重定向、是否含 password 表单、简短 body 指纹（长度/标题）。

---

## 4. 与漏洞验证的衔接

- 框架 consolidation 只接受**执行证据里出现过**的 `login_url` / 表单字段 / 凭据线索。  
- 你在 recon 中打印的登录 URL、字段名（如 `username`/`password`）、一次失败或成功登录的响应，是后续 `credential` / 已认证 FINDING 的唯一合法来源。  
- **禁止**在未请求过的路径上「猜」登录接口写进结论。  
- 发现弱口令或认证差分后：用代码打印可复现证据；最终 verdict 由框架 HTTP 验证给出，不要用模型叙事升级。

---

## 5. 资产图输出（约定格式）

在已收集到足够入口信息时，在代码 stdout 或助手文本中输出（便于后续轮次引用）：

```text
=== PENTGO ASSET MAP ===
target: https://example.test/
hosts_in_scope:
  - example.test
entries:
  - kind: login
    method: GET
    url: https://example.test/login
    evidence: form fields username,password; status 200
  - kind: api
    url: https://example.test/api/v1/
    evidence: status 401
  - kind: admin
    url: https://example.test/admin/
    evidence: status 302 location=/login
notes: homepage link /login; no password form on /
=== END PENTGO ASSET MAP ===
```

`kind` 建议：`login` | `api` | `admin` | `app` | `other`。  
框架当前**不解析**此块做自动扫描；它是给你自己与后续轮次的结构化证据。

---

## 6. 工具与实现提示

- 使用 Python `urllib` / `http.client` 或 Bash `curl -sS -D-`；需要会话时用 cookie jar，但 recon 阶段以**发现**为主。  
- 跟随重定向时打印跳转链；注意不要跟到 scope 外 host。  
- 大响应截断后打印标题、表单摘要、匹配 login 的片段即可。  
- 环境若有 `dig`/`host` 且 intent 允许子域，可做**有限**解析；否则跳过。

---

## 7. 完成条件（recon 子目标）

在宽泛「评估该站」类任务中，完成 recon 子目标至少满足：

1. 根 URL 可复现的存活证据；  
2. 已尝试同 host 入口发现并留下输出；  
3. 有或无登录面均已在 ASSET MAP 或等价输出中写明；  
4. 未越权、未爆破、未破坏性写。

然后可继续深测或在证据足够时给出普通助手结论。
