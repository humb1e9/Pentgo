# 批量迁移 Web hack-skills 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 bingo 的 30 个 **Web 渗透**相关 hack-skills 迁入 PentGo `skills/`，让 `SKILL_LOAD` / catalog 拥有真正的 Web 攻击知识面。同时把 registry 从"每 skill 手写 embed 变量 + map 条目"重构为 **`embed.FS` 整目录 + 描述 map** 机制，使加 skill 只需复制目录 + 加一行描述，避免 100+ skill 时的巨型易错 diff。

**Architecture:** `skills/registry.go` 保留 `Catalog()`/`Names()`/`Load()` 三个导出 API 与语义不变（`Load` 仍拒绝未知名与路径穿越）。内部改为：`//go:embed <name>/SKILL.md ...` 汇入单个 `embed.FS`；`descriptions map[string]string` 是**已登记 skill 的唯一真源**（catalog/names 遍历它，Load 先校验名在 map 中再从 FS 读取，天然拒绝 `../recon`、`recon/SKILL.md`）。只迁**只读 Markdown 知识**，不迁 bingo 的 Python 工具实现。

**Tech Stack:** Go 1.25 标准库（`embed`）；沿用现有 `skills` 包与 TDD/符号链接测试布局。

## Global Constraints

- 模块名 `pentgo`；Go 版本 `go 1.25.0`。
- 生产源码放 `skills/`；测试真身放 `tests/_packages/skills/`，源码目录建**相对符号链接**（`skills/registry_test.go -> ../tests/_packages/skills/registry_test.go`，以现有链接形态为准）。
- 测试 package 与被测包同名，不使用外部 `_test` 包。
- **只迁只读 Markdown**：不迁 bingo Python 工具、不迁伴随文件（已确认这 30 个目录仅含 `SKILL.md`、无 `SCENARIOS.md` 等悬空引用）。
- **不引入越狱 / 伪造授权构造**：已对 30 个候选扫描确认无此类措辞与 bingo 专有路径引用；执行者复制后须再扫一遍（Task 3）。
- skill 名须匹配 `SKILL_LOAD` 正则 `^[a-z][a-z0-9_-]*$`：`401-403-bypass-techniques` 以数字开头**不合规**，迁入时重命名为 `http-403-bypass`。
- `maxSkillBytes` 从 16000 提升到 **32000**（容纳最大的 `deserialization-insecure` 24635B、`path-traversal-lfi` 24562B 不被截断；正文仅在模型显式 `SKILL_LOAD` 时注入历史一次，非每轮）。
- 重构不改 `Catalog()`/`Names()`/`Load()` 的签名与既有行为（含拒绝路径穿越）。
- 每步结束运行该步列出的测试；每个 Task 至少一次提交。

## 迁移清单（30 个，源 → 目标名）

源均在 `bingo/skills/hack-skills/<dir>/SKILL.md`，目标为 `skills/<name>/SKILL.md`。除 `http-403-bypass` 外目标名同源目录名：

注入类：`sqli-sql-injection`、`cmdi-command-injection`、`ssti-server-side-template-injection`、`xxe-xml-external-entity`、`xss-cross-site-scripting`、`path-traversal-lfi`、`expression-language-injection`、`jndi-injection`、`crlf-injection`、`http-parameter-pollution`
请求/协议类：`ssrf-server-side-request-forgery`、`csrf-cross-site-request-forgery`、`request-smuggling`、`http-host-header-attacks`、`open-redirect`、`cors-cross-origin-misconfiguration`、`web-cache-deception`、`websocket-security`、`http2-specific-attacks`
认证/授权类：`idor-broken-object-authorization`、`authbypass-authentication-flaws`、`http-403-bypass`（源 `401-403-bypass-techniques`）、`jwt-oauth-token-attacks`、`oauth-oidc-misconfiguration`
其它 Web：`deserialization-insecure`、`upload-insecure-files`、`prototype-pollution`、`csp-bypass-advanced`、`subdomain-takeover`、`race-condition`

迁移后连同现有 5 个（`recon`、`terminal`、`waf-bypass`、`nosql-injection`、`type-juggling`）共 **35** 个已登记 skill。

---
### Task 1: registry 重构为 embed.FS + 描述 map（行为不变、仍 5 个 skill）

**Files:**
- Modify: `skills/registry.go`
- Test: `tests/_packages/skills/registry_test.go`（已存在，本 Task 不改断言——用来证明行为不变）

**Interfaces:**
- Produces（签名/行为均不变）：
  - `Catalog() []Skill`、`Names() []string`、`Load(name string) (string, error)`
  - `Load` 对未知名、`../recon`、`recon/SKILL.md` 仍返回 error
  - `maxSkillBytes == 32000`
- 内部改为：单个 `//go:embed` 汇入的 `skillFS embed.FS` + `descriptions map[string]string`（唯一真源）。

- [ ] **Step 1: 先跑现有测试确认基线全绿**

Run: `go test ./skills -count=1`
Expected: PASS（重构前基线；本 Task 目标是重构后仍全绿、断言一字不改）。

- [ ] **Step 2: 重写 `skills/registry.go`**

整体替换为（保留现有 5 个 skill 的目录与描述文本）：

```go
package skills

import (
	"embed"
	"fmt"
	"sort"
)

const maxSkillBytes = 32000

//go:embed recon/SKILL.md
//go:embed terminal/SKILL.md
//go:embed waf-bypass/SKILL.md
//go:embed nosql-injection/SKILL.md
//go:embed type-juggling/SKILL.md
var skillFS embed.FS

// descriptions 是已登记 Skill 的唯一真源：名称 -> 目录清单描述。
var descriptions = map[string]string{
	"recon":           "信息收集方法论：以已回灌的执行输出为依据，逐步减少目标未知信息。",
	"terminal":        "终端 Agent 通用准则：只把已执行并回灌的输出当作证据。",
	"waf-bypass":      "WAF 绕过技法：编码/大小写/注释/分块/HTTP 层面的检测规避手法。",
	"nosql-injection": "NoSQL 注入：MongoDB/Redis 等运算符注入、认证绕过与盲注提取。",
	"type-juggling":   "类型混淆：PHP 松散比较、魔术哈希与 JSON 类型强制导致的认证/逻辑绕过。",
}

// Skill 是可加载 Skill 的目录条目，不含正文。
type Skill struct {
	Name        string
	Description string
}

// Catalog 返回按名称升序排列的可加载 Skill 目录（名称与描述，不含正文）。
func Catalog() []Skill {
	catalog := make([]Skill, 0, len(descriptions))
	for name, description := range descriptions {
		catalog = append(catalog, Skill{Name: name, Description: description})
	}
	sort.Slice(catalog, func(i, j int) bool { return catalog[i].Name < catalog[j].Name })
	return catalog
}

// Names 返回可由 Runtime 加载的 Skill 名称（升序）。
func Names() []string {
	names := make([]string, 0, len(descriptions))
	for name := range descriptions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Load 返回一个注册的只读 Skill，内容被限制为模型上下文上限。
// 先校验名在 descriptions 中，天然拒绝未知名与路径穿越（如 "../recon"）。
func Load(name string) (string, error) {
	if _, ok := descriptions[name]; !ok {
		return "", fmt.Errorf("unknown skill %q", name)
	}
	content, err := skillFS.ReadFile(name + "/SKILL.md")
	if err != nil {
		return "", fmt.Errorf("load skill %q: %w", name, err)
	}
	if len(content) > maxSkillBytes {
		return string(content[:maxSkillBytes]), nil
	}
	return string(content), nil
}
```

- [ ] **Step 3: 跑测试确认行为不变**

Run: `go test ./skills -count=1`
Expected: PASS（`TestLoadReturnsRegisteredReadOnlySkill`、`TestLoadRejectsUnknownAndTraversalNames`、`TestNamesListsRegisteredSkills`（5 个）、`TestCatalogListsSkillsWithDescriptions`（5 个）、`TestLoadMigratedSkill` 全绿，断言未改）。

- [ ] **Step 4: Commit**

```bash
gofmt -w skills/registry.go
git add skills/registry.go
git commit -m "refactor: registry uses embed.FS with description map"
```

---

### Task 2: 迁入 30 个 Web hack-skills

**Files:**
- Create: `skills/<name>/SKILL.md` × 30（见迁移清单）
- Modify: `skills/registry.go`（追加 30 个 embed 行 + 30 条描述）
- Test: `tests/_packages/skills/registry_test.go`（改数量/成员断言）

**Interfaces:**
- Produces：`descriptions` 含 35 项；`Names()`/`Catalog()` 返回 35 个（升序）；30 个新 skill 均可 `Load` 且非空。

- [ ] **Step 1: 复制 30 个 SKILL.md（含 http-403-bypass 重命名）**

从仓库根运行（每条一个 skill，避免 zsh 分词问题）：

```bash
cd /home/kali/PentGo
SRC=bingo/skills/hack-skills
for n in sqli-sql-injection cmdi-command-injection ssti-server-side-template-injection xxe-xml-external-entity xss-cross-site-scripting path-traversal-lfi expression-language-injection jndi-injection crlf-injection http-parameter-pollution ssrf-server-side-request-forgery csrf-cross-site-request-forgery request-smuggling http-host-header-attacks open-redirect cors-cross-origin-misconfiguration web-cache-deception websocket-security http2-specific-attacks idor-broken-object-authorization authbypass-authentication-flaws jwt-oauth-token-attacks oauth-oidc-misconfiguration deserialization-insecure upload-insecure-files prototype-pollution csp-bypass-advanced subdomain-takeover race-condition; do
  mkdir -p "skills/$n"
  cp "$SRC/$n/SKILL.md" "skills/$n/SKILL.md"
done
mkdir -p skills/http-403-bypass
cp "$SRC/401-403-bypass-techniques/SKILL.md" skills/http-403-bypass/SKILL.md
```

（若 shell 为 zsh，用 `for n in ...; do` 逐词循环即可，zsh 会对字面量列表分词。）

- [ ] **Step 2: Write the failing test（改断言到 35）**

`tests/_packages/skills/registry_test.go`：

把 `TestNamesListsRegisteredSkills` 的 `want` 与逐项比较改为长度 + 关键成员检查：

```go
func TestNamesListsRegisteredSkills(t *testing.T) {
	names := Names()
	if len(names) != 35 {
		t.Fatalf("len(names) = %d, want 35", len(names))
	}
	index := make(map[string]bool, len(names))
	for _, n := range names {
		index[n] = true
	}
	for _, must := range []string{"recon", "terminal", "waf-bypass", "sqli-sql-injection", "ssrf-server-side-request-forgery", "http-403-bypass", "race-condition"} {
		if !index[must] {
			t.Fatalf("names missing %q: %q", must, names)
		}
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("names not sorted ascending at %d: %q", i, names)
		}
	}
}
```

把 `TestCatalogListsSkillsWithDescriptions` 的 `len(catalog) != 5` 改为 `!= 35`，删除 `catalog[0].Name != "nosql-injection"` 那条脆弱的首项断言（改由上面的排序检查覆盖），保留"每项描述非空"：

```go
func TestCatalogListsSkillsWithDescriptions(t *testing.T) {
	catalog := Catalog()
	if len(catalog) != 35 {
		t.Fatalf("catalog length = %d, want 35", len(catalog))
	}
	for _, skill := range catalog {
		if strings.TrimSpace(skill.Description) == "" {
			t.Fatalf("skill %q has empty description", skill.Name)
		}
	}
}
```

在 `TestLoadMigratedSkill` 的名单里补几个新迁入项做加载冒烟：

```go
	for _, name := range []string{"waf-bypass", "nosql-injection", "type-juggling", "sqli-sql-injection", "xxe-xml-external-entity", "http-403-bypass", "deserialization-insecure"} {
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./skills -count=1`
Expected: FAIL（`descriptions` 仍 5 项、embed 未含新目录；`http-403-bypass` 等 Load 失败或数量不符）。

- [ ] **Step 4: registry.go 追加 30 个 embed 行 + 30 条描述**

在现有 5 行 `//go:embed` 后追加（同一 `skillFS`）：

```go
//go:embed sqli-sql-injection/SKILL.md
//go:embed cmdi-command-injection/SKILL.md
//go:embed ssti-server-side-template-injection/SKILL.md
//go:embed xxe-xml-external-entity/SKILL.md
//go:embed xss-cross-site-scripting/SKILL.md
//go:embed path-traversal-lfi/SKILL.md
//go:embed expression-language-injection/SKILL.md
//go:embed jndi-injection/SKILL.md
//go:embed crlf-injection/SKILL.md
//go:embed http-parameter-pollution/SKILL.md
//go:embed ssrf-server-side-request-forgery/SKILL.md
//go:embed csrf-cross-site-request-forgery/SKILL.md
//go:embed request-smuggling/SKILL.md
//go:embed http-host-header-attacks/SKILL.md
//go:embed open-redirect/SKILL.md
//go:embed cors-cross-origin-misconfiguration/SKILL.md
//go:embed web-cache-deception/SKILL.md
//go:embed websocket-security/SKILL.md
//go:embed http2-specific-attacks/SKILL.md
//go:embed idor-broken-object-authorization/SKILL.md
//go:embed authbypass-authentication-flaws/SKILL.md
//go:embed http-403-bypass/SKILL.md
//go:embed jwt-oauth-token-attacks/SKILL.md
//go:embed oauth-oidc-misconfiguration/SKILL.md
//go:embed deserialization-insecure/SKILL.md
//go:embed upload-insecure-files/SKILL.md
//go:embed prototype-pollution/SKILL.md
//go:embed csp-bypass-advanced/SKILL.md
//go:embed subdomain-takeover/SKILL.md
//go:embed race-condition/SKILL.md
```

在 `descriptions` map 追加 30 条（描述据各 SKILL.md frontmatter 精炼为中文）：

```go
	"sqli-sql-injection":                  "SQL 注入：输入进入查询/认证/排序/过滤时的注入，含数据库指纹、盲注与带外。",
	"cmdi-command-injection":              "命令注入：输入进入 shell/进程执行/转换管道，含盲注与带外命令回连。",
	"ssti-server-side-template-injection": "服务端模板注入：模板表达式/服务端渲染求值攻击者可控内容。",
	"xxe-xml-external-entity":             "XXE：XML/SVG/OOXML/SOAP 解析外部实体读文件、探内网、带外外传。",
	"xss-cross-site-scripting":            "XSS：用户内容进入 HTML/属性/JS/DOM 汇聚点的多上下文注入。",
	"path-traversal-lfi":                  "路径穿越/LFI：文件路径、下载、包含、解压、wrapper 暴露文件系统控制。",
	"expression-language-injection":       "表达式注入：Java EL/SpEL/OGNL/MVEL 在 Spring/Struts2 等求值可控输入。",
	"jndi-injection":                      "JNDI 注入：可控名称的 JNDI lookup（Log4j2/Spring）导致远程加载。",
	"crlf-injection":                      "CRLF 注入：输入进入响应头/重定向/Set-Cookie/日志，拆分或注入内容。",
	"http-parameter-pollution":            "HTTP 参数污染：重复键在服务器/代理/WAF/框架解析不一致导致绕过。",
	"ssrf-server-side-request-forgery":    "SSRF：服务端取 URL/解析主机，可诱导访问内网、云元数据、次级协议。",
	"csrf-cross-site-request-forgery":     "CSRF：状态变更流程、SameSite、JSON CSRF、登录 CSRF、OAuth state。",
	"request-smuggling":                   "请求走私：前端代理与源站对报文边界解析分歧（CL/TE、H2→H1）。",
	"http-host-header-attacks":            "Host 头攻击：应用信任 Host 生成 URL/路由/鉴权导致投毒与绕过。",
	"open-redirect":                       "开放重定向：URL 参数/表单/JS 汇聚点控制跳转到攻击者目标。",
	"cors-cross-origin-misconfiguration":  "CORS 配置错误：跨源信任、凭据读取、Origin 反射、预检策略缺陷。",
	"web-cache-deception":                 "Web 缓存欺骗/投毒：路径混淆或缓存键操纵使认证内容被缓存给他人。",
	"websocket-security":                  "WebSocket 安全：握手、CSWSH 跨站劫持与实时通道常见缺陷。",
	"http2-specific-attacks":              "HTTP/2 专项：二进制帧、HPACK、h2c 升级走私、伪首部注入、降级翻译。",
	"idor-broken-object-authorization":    "IDOR/对象越权：请求暴露对象标识、租户边界、可写字段、缺失对象级鉴权。",
	"authbypass-authentication-flaws":     "认证绕过：登录、密码重置、账户恢复、MFA 绕过、令牌可预测、会话边界。",
	"http-403-bypass":                     "401/403 绕过：路径变形、方法篡改、头注入、协议降级绕过访问控制。",
	"jwt-oauth-token-attacks":             "JWT/OAuth 令牌攻击：签名算法、密钥处理、声明滥用、bearer 与账户绑定。",
	"oauth-oidc-misconfiguration":         "OAuth/OIDC 配置错误：redirect_uri、state/nonce、PKCE、audience、回调绑定。",
	"deserialization-insecure":            "不安全反序列化：Java/PHP/Python 反序列化不可信数据导致 RCE/文件访问。",
	"upload-insecure-files":               "不安全文件上传：校验、存储路径、处理管道、覆盖风险与上传到 RCE。",
	"prototype-pollution":                 "原型污染：输入合并入对象污染 Object.prototype，Node/浏览器 gadget 到 RCE。",
	"csp-bypass-advanced":                 "CSP 绕过：策略弱点、可信端点滥用、nonce 泄露与 CSP 无法拦的外传通道。",
	"subdomain-takeover":                  "子域接管：悬空 CNAME/NS/MX 指向已释放云资源或未认领 SaaS 租户。",
	"race-condition":                      "竞态/TOCTOU：一次性操作、并发滥用、限流绕过、HTTP/2 单包攻击。",
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./skills -count=1`
Expected: PASS（35 个；新 skill 可加载非空；升序）。

- [ ] **Step 6: Commit**

```bash
gofmt -w skills/registry.go tests/_packages/skills/registry_test.go
git add skills/ tests/_packages/skills/registry_test.go
git commit -m "feat: migrate 30 web hack-skills into registry"
```

---

### Task 3: 安全复扫 + 全量验证

- [ ] **Step 1: 复扫迁入内容无越狱/伪造授权/悬空引用**

Run（对 `skills/` 下**新迁入的 30 个目录**）：

```bash
cd /home/kali/PentGo
grep -rniE "ignore (previous|above|prior)|you are now|jailbreak|pretend you|no restrictions|without (any )?(authorization|permission|consent)|developer mode" skills/ || echo "CLEAN: no jailbreak wording"
grep -rniE "bingo|/opt/bingo|parallel_runner|import bingo|hack-skills/" skills/ || echo "CLEAN: no bingo-proprietary refs"
grep -rEoh '\]\(\./[A-Za-z0-9_-]+\.(md|py|txt)\)' skills/ | sort -u || echo "CLEAN: no companion-file links"
```

Expected: 三条均 CLEAN（悬空引用一条若有输出，须核对被引用文件是否已迁入或删除该引用）。

- [ ] **Step 2: 全量回归**

```bash
go build ./...
go test ./...
go vet ./...
git diff --check
```

Expected: 全部通过；`go build`/`go vet` 无输出即成功。特别确认 `go build` 通过——`//go:embed` 目标缺失会编译失败，可反向验证 30 个文件均已就位。

- [ ] **Step 3: Commit（若复扫暴露需修项则补 commit，否则跳过）**

---

## 自查

- **Spec 覆盖**：机制重构为 embed.FS + 描述 map（Task 1）✓；迁入 30 个 Web skill、35 项登记（Task 2）✓；安全复扫 + 全量验证（Task 3）✓。
- **占位符扫描**：无 TODO/TBD；30 条描述均为据 frontmatter 精炼的完整中文；embed 行与描述 map 键一一对应。
- **类型一致性**：`descriptions` 键（35）↔ `//go:embed` 目录（35）↔ `Names()`/`Catalog()` 长度断言（35）自洽；`http-403-bypass` 在重命名、embed、描述、测试成员断言四处一致；`maxSkillBytes=32000` > 最大 skill 24635B。
- **行为不变**：Task 1 不改测试断言即证明 `Catalog/Names/Load`（含拒绝穿越）语义不变；Task 2 仅扩充数据。

## 集成前提（执行者须先确认）

1. `skills/registry_test.go` 为指向 `tests/_packages/skills/registry_test.go` 的相对符号链接；改断言直接改真身。
2. `//go:embed` 目标文件必须先复制到位再编译，否则 `go build` 失败——Task 2 Step 1 先复制、Step 4 再加 embed 行。
3. `http-403-bypass` 目标名与源目录 `401-403-bypass-techniques` 不同名（数字开头不合规），复制、embed、描述、测试四处均用新名。
4. Task 1 与 Task 2 拆分提交：先证明重构行为不变（5 个），再扩到 35 个，便于回溯。
5. catalog 会注入每轮系统提示词；35 条描述均为一行精炼中文，控制 prompt 体量。

