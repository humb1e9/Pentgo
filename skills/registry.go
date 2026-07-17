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
var skillFS embed.FS

// descriptions 是已登记 Skill 的唯一真源：名称 -> 目录清单描述。
var descriptions = map[string]string{
	"recon":           "信息收集方法论：以已回灌的执行输出为依据，逐步减少目标未知信息。",
	"terminal":        "终端 Agent 通用准则：只把已执行并回灌的输出当作证据。",
	"waf-bypass":      "WAF 绕过技法：编码/大小写/注释/分块/HTTP 层面的检测规避手法。",
	"nosql-injection": "NoSQL 注入：MongoDB/Redis 等运算符注入、认证绕过与盲注提取。",
	"type-juggling":   "类型混淆：PHP 松散比较、魔术哈希与 JSON 类型强制导致的认证/逻辑绕过。",

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
