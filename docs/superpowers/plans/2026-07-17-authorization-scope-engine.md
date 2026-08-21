# Authorization & Scope Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 PentGo 终端 Agent Runtime 增加一道执行前的授权门：把每个代码块限制在授权目标范围内，并拦截破坏性操作，拦截项永不执行且以可回灌文本要求模型重写。

**Architecture:** 在 `internal/runtime` 新增 `Scope`（主机范围判定）、破坏性操作策略和 `Authorizer`（组合两者产出决策）。`Runner` 在 `Preflight` 之后、计算 `approved` 之前运行授权门，被拦截的块降级为未批准的 `PreflightResult`（复用现有"拒绝块记录 evidence + 回灌重写"通路），并追加 `authorization_blocked` 恢复事件。`app` 层从配置和会话目标构造 `Authorizer` 与 scope 输入并传入 `Runner`。

**Tech Stack:** Go 1.25 标准库（`net`、`net/url`、`regexp`）；沿用现有 `runtime`、`config`、`app` 包与 TDD/符号链接测试布局。

## Global Constraints

- 模块名为 `pentgo`；Go 版本 `go 1.25.0`。
- 生产源码放在 `internal/<pkg>/`；测试文件真身放在 `tests/_packages/internal/<pkg>/<name>_test.go`，并在 `internal/<pkg>/` 建**相对符号链接** `../../tests/_packages/internal/<pkg>/<name>_test.go`（与现有 `blocks_test.go` 一致）。
- 测试 package 与被测包同名（如 `package runtime`、`package config`），不使用外部 `_test` 包。
- 安全默认：授权**默认开启**、**默认禁止破坏性操作**、**默认允许 localhost/私网**。配置里布尔用 `*bool` 表达"未设置=默认"，`allow_destructive` 用普通 `bool`（零值 false 即安全默认）。
- 授权关闭（`Authorizer == nil` 或 `enabled=false`）时行为与当前 Runtime 完全一致。
- 不改动 `Preflight`、`renderPreflightRejections`、执行器和报告的现有措辞与已有测试断言。
- scope 主机提取只从代码中的 `http(s)://` URL 解析主机（低误报），这是尽力而为的静态策略门，不是沙箱；在 README 注明此限制。
- 每步结束运行该步列出的测试；频繁提交，每个 Task 至少一次提交。

---

### Task 1: 授权配置模型与安全默认访问器

**Files:**
- Modify: `internal/config/config.go`
- Test: `tests/_packages/internal/config/config_test.go` (已存在，追加测试)

**Interfaces:**
- Produces:
  - `type AuthorizationConfig struct { Enabled *bool; AllowDestructive bool; AllowPrivateHosts *bool; AllowedHosts []string }`
    tags: `json:"enabled,omitempty"`、`json:"allow_destructive,omitempty"`、`json:"allow_private_hosts,omitempty"`、`json:"allowed_hosts,omitempty"`
  - `func (c AuthorizationConfig) IsEnabled() bool`（nil→true）
  - `func (c AuthorizationConfig) PrivateAllowed() bool`（nil→true）
  - `AgentConfig` 新增字段 `Authorization AuthorizationConfig` tag `json:"authorization"`

- [ ] **Step 1: Write the failing test**

追加到 `tests/_packages/internal/config/config_test.go`：

```go
func TestAuthorizationDefaultsAreSafe(t *testing.T) {
	auth := Default().Agent.Authorization
	if !auth.IsEnabled() {
		t.Fatal("authorization should default to enabled")
	}
	if auth.AllowDestructive {
		t.Fatal("destructive operations should default to blocked")
	}
	if !auth.PrivateAllowed() {
		t.Fatal("private hosts should default to allowed")
	}
	if len(auth.AllowedHosts) != 0 {
		t.Fatalf("allowed hosts default = %v", auth.AllowedHosts)
	}
}

func TestAuthorizationConfigOverrides(t *testing.T) {
	disabled := false
	privateOff := false
	cfg := AuthorizationConfig{Enabled: &disabled, AllowDestructive: true, AllowPrivateHosts: &privateOff}
	if cfg.IsEnabled() || !cfg.AllowDestructive || cfg.PrivateAllowed() {
		t.Fatalf("override accessors = %+v", cfg)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config -run 'TestAuthorization' -count=1`
Expected: FAIL（编译错误：`AuthorizationConfig`、`IsEnabled`、`PrivateAllowed`、`Agent.Authorization` 未定义）。

- [ ] **Step 3: Write minimal implementation**

在 `internal/config/config.go` 的 `AgentConfig` 结构体末尾（`Anthropic` 字段之后）追加字段：

```go
	Authorization             AuthorizationConfig `json:"authorization"`
```

在 `ModelProviderConfig` 定义之后新增：

```go
// AuthorizationConfig 描述执行前授权门的开关与范围策略。
// 布尔指针字段为 nil 时表示使用安全默认值。
type AuthorizationConfig struct {
	Enabled           *bool    `json:"enabled,omitempty"`
	AllowDestructive  bool     `json:"allow_destructive,omitempty"`
	AllowPrivateHosts *bool    `json:"allow_private_hosts,omitempty"`
	AllowedHosts      []string `json:"allowed_hosts,omitempty"`
}

// IsEnabled 在未显式配置时默认开启授权门。
func (c AuthorizationConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// PrivateAllowed 在未显式配置时默认允许 localhost 与私网地址。
func (c AuthorizationConfig) PrivateAllowed() bool {
	return c.AllowPrivateHosts == nil || *c.AllowPrivateHosts
}
```

`defaultAgentConfig` 无需设置 `Authorization`（零值经访问器即安全默认）。`normalizeAgentConfig` 无需改动。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config -run 'TestAuthorization' -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/config/config.go tests/_packages/internal/config/config_test.go
git add internal/config/config.go tests/_packages/internal/config/config_test.go
git commit -m "feat: add authorization config with safe defaults"
```

---

### Task 2: Scope 主机范围判定

**Files:**
- Create: `internal/runtime/scope.go`
- Create: `tests/_packages/internal/runtime/scope_test.go`
- Create symlink: `internal/runtime/scope_test.go -> ../../tests/_packages/internal/runtime/scope_test.go`

**Interfaces:**
- Produces:
  - `type Scope struct { targetHost string; allowedHosts []string; allowPrivate bool }`
  - `func NewScope(targetHost string, allowedHosts []string, allowPrivate bool) Scope`
  - `func (s Scope) HostAllowed(host string) bool`

- [ ] **Step 1: Write the failing test**

创建 `tests/_packages/internal/runtime/scope_test.go`：

```go
package runtime

import "testing"

func TestScopeAllowsTargetAndSubdomains(t *testing.T) {
	scope := NewScope("example.com", nil, false)
	for _, host := range []string{"example.com", "api.example.com", "a.b.example.com"} {
		if !scope.HostAllowed(host) {
			t.Fatalf("host %q should be in scope", host)
		}
	}
}

func TestScopeBlocksOtherHosts(t *testing.T) {
	scope := NewScope("example.com", nil, false)
	for _, host := range []string{"evil.com", "notexample.com", "example.com.evil.com"} {
		if scope.HostAllowed(host) {
			t.Fatalf("host %q should be out of scope", host)
		}
	}
}

func TestScopeAllowsExtraAllowedHosts(t *testing.T) {
	scope := NewScope("example.com", []string{"cdn.trusted.net"}, false)
	if !scope.HostAllowed("cdn.trusted.net") {
		t.Fatal("explicitly allowed host should be in scope")
	}
	if !scope.HostAllowed("x.cdn.trusted.net") {
		t.Fatal("subdomain of allowed host should be in scope")
	}
}

func TestScopePrivateHostsGatedByFlag(t *testing.T) {
	blocked := NewScope("example.com", nil, false)
	allowed := NewScope("example.com", nil, true)
	for _, host := range []string{"localhost", "127.0.0.1", "10.0.0.5", "192.168.1.1", "::1"} {
		if blocked.HostAllowed(host) {
			t.Fatalf("private host %q must be blocked when allowPrivate=false", host)
		}
		if !allowed.HostAllowed(host) {
			t.Fatalf("private host %q must be allowed when allowPrivate=true", host)
		}
	}
}

func TestScopeAllowsEmptyHost(t *testing.T) {
	if !NewScope("example.com", nil, false).HostAllowed("") {
		t.Fatal("empty host (relative URL) should be allowed")
	}
}
```

创建符号链接：

```bash
ln -s ../../tests/_packages/internal/runtime/scope_test.go internal/runtime/scope_test.go
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime -run 'TestScope' -count=1`
Expected: FAIL（编译错误：`Scope`、`NewScope`、`HostAllowed` 未定义）。

- [ ] **Step 3: Write minimal implementation**

创建 `internal/runtime/scope.go`：

```go
package runtime

import (
	"net"
	"strings"
)

// Scope 判定一个主机是否落在授权范围内。
type Scope struct {
	targetHost   string
	allowedHosts []string
	allowPrivate bool
}

// NewScope 以目标主机、附加允许主机与私网策略构造范围。
func NewScope(targetHost string, allowedHosts []string, allowPrivate bool) Scope {
	normalized := make([]string, 0, len(allowedHosts))
	for _, host := range allowedHosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host != "" {
			normalized = append(normalized, host)
		}
	}
	return Scope{
		targetHost:   strings.ToLower(strings.TrimSpace(targetHost)),
		allowedHosts: normalized,
		allowPrivate: allowPrivate,
	}
}

// HostAllowed 判定给定主机是否可访问。空主机（相对 URL）视为同源放行。
func (s Scope) HostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return true
	}
	if isLoopbackOrPrivate(host) {
		return s.allowPrivate
	}
	if matchHost(host, s.targetHost) {
		return true
	}
	for _, allowed := range s.allowedHosts {
		if matchHost(host, allowed) {
			return true
		}
	}
	return false
}

// matchHost 判断 host 是否等于 base 或为其子域。
func matchHost(host, base string) bool {
	if base == "" {
		return false
	}
	return host == base || strings.HasSuffix(host, "."+base)
}

func isLoopbackOrPrivate(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/runtime -run 'TestScope' -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/runtime/scope.go tests/_packages/internal/runtime/scope_test.go
git add internal/runtime/scope.go tests/_packages/internal/runtime/scope_test.go internal/runtime/scope_test.go
git commit -m "feat: add scope host matching for authorization"
```

---

### Task 3: Authorizer 决策（破坏性操作 + 范围）

**Files:**
- Create: `internal/runtime/authorization.go`
- Create: `tests/_packages/internal/runtime/authorization_test.go`
- Create symlink: `internal/runtime/authorization_test.go -> ../../tests/_packages/internal/runtime/authorization_test.go`

**Interfaces:**
- Consumes: `CodeBlock`（`blocks.go`）、`Scope`（Task 2）。
- Produces:
  - `type Decision struct { Allowed bool; Reason string }`
  - `type Authorizer struct { allowDestructive bool }`
  - `func NewAuthorizer(allowDestructive bool) *Authorizer`
  - `func (a *Authorizer) Authorize(block CodeBlock, scope Scope) Decision`（nil 接收者返回 `Decision{Allowed: true}`）
  - `func extractHosts(code string) []string`（从代码中的 http(s) URL 解析出的裸主机名列表）

- [ ] **Step 1: Write the failing test**

创建 `tests/_packages/internal/runtime/authorization_test.go`：

```go
package runtime

import (
	"strings"
	"testing"
)

func TestAuthorizerBlocksDestructiveSQL(t *testing.T) {
	authz := NewAuthorizer(false)
	scope := NewScope("example.com", nil, true)
	code := "import requests\nrequests.get('https://example.com/?q=1 UNION SELECT 1; DROP TABLE users')\n"
	decision := authz.Authorize(CodeBlock{Index: 1, Language: LanguagePython, Code: code}, scope)
	if decision.Allowed || !strings.Contains(strings.ToLower(decision.Reason), "destructive") {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestAuthorizerAllowsReadOnlySQL(t *testing.T) {
	authz := NewAuthorizer(false)
	scope := NewScope("example.com", nil, true)
	code := "import requests\nrequests.get('https://example.com/?id=1 UNION SELECT username FROM users')\n"
	decision := authz.Authorize(CodeBlock{Index: 1, Language: LanguagePython, Code: code}, scope)
	if !decision.Allowed {
		t.Fatalf("read-only SELECT should be allowed: %+v", decision)
	}
}

func TestAuthorizerBlocksDestructiveShell(t *testing.T) {
	authz := NewAuthorizer(false)
	scope := NewScope("example.com", nil, true)
	decision := authz.Authorize(CodeBlock{Index: 1, Language: LanguageShell, Code: "rm -rf /tmp/loot"}, scope)
	if decision.Allowed || !strings.Contains(strings.ToLower(decision.Reason), "destructive") {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestAuthorizerBlocksOutOfScopeHost(t *testing.T) {
	authz := NewAuthorizer(false)
	scope := NewScope("example.com", nil, true)
	code := "import requests\nrequests.get('https://evil.com/steal')\n"
	decision := authz.Authorize(CodeBlock{Index: 1, Language: LanguagePython, Code: code}, scope)
	if decision.Allowed || !strings.Contains(strings.ToLower(decision.Reason), "scope") {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestAuthorizerAllowsInScopeHost(t *testing.T) {
	authz := NewAuthorizer(false)
	scope := NewScope("example.com", nil, true)
	code := "import requests\nrequests.get('https://api.example.com/status')\n"
	decision := authz.Authorize(CodeBlock{Index: 1, Language: LanguagePython, Code: code}, scope)
	if !decision.Allowed {
		t.Fatalf("in-scope host should be allowed: %+v", decision)
	}
}

func TestAuthorizerAllowsDestructiveWhenConfigured(t *testing.T) {
	authz := NewAuthorizer(true)
	scope := NewScope("example.com", nil, true)
	decision := authz.Authorize(CodeBlock{Index: 1, Language: LanguageShell, Code: "rm -rf /tmp/loot"}, scope)
	if !decision.Allowed {
		t.Fatalf("destructive should be allowed when configured: %+v", decision)
	}
}

func TestNilAuthorizerAllows(t *testing.T) {
	var authz *Authorizer
	if !authz.Authorize(CodeBlock{Index: 1, Language: LanguageShell, Code: "rm -rf /"}, NewScope("example.com", nil, false)).Allowed {
		t.Fatal("nil authorizer must allow")
	}
}

func TestExtractHostsFromCode(t *testing.T) {
	hosts := extractHosts("a='https://example.com/x'\nb=\"http://evil.com:8080/y\"\nc='not a url'")
	got := strings.Join(hosts, ",")
	if !strings.Contains(got, "example.com") || !strings.Contains(got, "evil.com") {
		t.Fatalf("hosts = %v", hosts)
	}
}
```

创建符号链接：

```bash
ln -s ../../tests/_packages/internal/runtime/authorization_test.go internal/runtime/authorization_test.go
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime -run 'TestAuthorizer|TestNilAuthorizer|TestExtractHosts' -count=1`
Expected: FAIL（编译错误：`Authorizer`、`NewAuthorizer`、`Authorize`、`Decision`、`extractHosts` 未定义）。

- [ ] **Step 3: Write minimal implementation**

创建 `internal/runtime/authorization.go`：

```go
package runtime

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// destructiveSQL 匹配会写入或破坏数据的 SQL 操作。
var destructiveSQL = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bINSERT\s+(INTO|IGNORE)\b`),
	regexp.MustCompile(`(?i)\bUPDATE\s+\w+\s+SET\b`),
	regexp.MustCompile(`(?i)\bDELETE\s+FROM\b`),
	regexp.MustCompile(`(?i)\bDROP\s+(TABLE|DATABASE|INDEX|VIEW|PROCEDURE|SCHEMA)\b`),
	regexp.MustCompile(`(?i)\bTRUNCATE\s+(TABLE\s+)?\w+`),
	regexp.MustCompile(`(?i)\bCREATE\s+(TABLE|DATABASE|USER|SCHEMA)\b`),
	regexp.MustCompile(`(?i)\bALTER\s+(TABLE|USER|DATABASE)\b`),
	regexp.MustCompile(`(?i)\b(GRANT|REVOKE)\s+\w+\s+ON\b`),
	regexp.MustCompile(`(?i)\bRENAME\s+TABLE\b`),
	regexp.MustCompile(`(?i)\bREPLACE\s+INTO\b`),
}

// destructiveShell 匹配会破坏本地或远端系统的命令。
var destructiveShell = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\brm\s+-[a-z]*r[a-z]*f`),
	regexp.MustCompile(`(?i)\brm\s+-[a-z]*f[a-z]*r`),
	regexp.MustCompile(`(?i)\bmkfs\b`),
	regexp.MustCompile(`(?i)\bdd\s+.*of=/dev/`),
	regexp.MustCompile(`(?i)>\s*/dev/sd[a-z]`),
	regexp.MustCompile(`(?i)\b(shutdown|reboot|halt|poweroff)\b`),
	regexp.MustCompile(`:\(\)\s*\{\s*:\|:&\s*\}`),
}

var urlPattern = regexp.MustCompile(`(?i)https?://[^\s'"` + "`" + `<>)\\]+`)

// Decision 是单个代码块的授权判定结果。
type Decision struct {
	Allowed bool
	Reason  string
}

// Authorizer 在执行前对代码块施加破坏性操作与范围策略。
type Authorizer struct {
	allowDestructive bool
}

// NewAuthorizer 创建一个授权器。allowDestructive 为 false 时拦截写/破坏操作。
func NewAuthorizer(allowDestructive bool) *Authorizer {
	return &Authorizer{allowDestructive: allowDestructive}
}

// Authorize 检查破坏性操作与目标范围。nil 接收者始终放行。
func (a *Authorizer) Authorize(block CodeBlock, scope Scope) Decision {
	if a == nil {
		return Decision{Allowed: true}
	}
	if !a.allowDestructive {
		if matchAny(block.Code, destructiveSQL) {
			return Decision{Allowed: false, Reason: "blocked destructive SQL operation"}
		}
		if matchAny(block.Code, destructiveShell) {
			return Decision{Allowed: false, Reason: "blocked destructive system command"}
		}
	}
	for _, host := range extractHosts(block.Code) {
		if !scope.HostAllowed(host) {
			return Decision{Allowed: false, Reason: fmt.Sprintf("host %q is out of authorized scope", host)}
		}
	}
	return Decision{Allowed: true}
}

func matchAny(code string, patterns []*regexp.Regexp) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(code) {
			return true
		}
	}
	return false
}

// extractHosts 从代码中的 http(s) URL 解析出裸主机名。
func extractHosts(code string) []string {
	matches := urlPattern.FindAllString(code, -1)
	seen := make(map[string]bool, len(matches))
	hosts := make([]string, 0, len(matches))
	for _, raw := range matches {
		parsed, err := url.Parse(raw)
		if err != nil {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		hosts = append(hosts, host)
	}
	return hosts
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/runtime -run 'TestAuthorizer|TestNilAuthorizer|TestExtractHosts' -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/runtime/authorization.go tests/_packages/internal/runtime/authorization_test.go
git add internal/runtime/authorization.go tests/_packages/internal/runtime/authorization_test.go internal/runtime/authorization_test.go
git commit -m "feat: add authorizer for destructive ops and scope"
```

---

### Task 4: 在 Runner 循环中接入授权门

**Files:**
- Modify: `internal/runtime/runner.go`
- Test: `tests/_packages/internal/runtime/runner_test.go` (已存在，追加测试)

**Interfaces:**
- Consumes: `Authorizer`、`Scope`（Task 2/3）；现有 `PreflightResult`、`renderPreflightRejections`、`session.AddEvent`、`session.Target.Canonical`。
- Produces:
  - `RunnerConfig` 新增字段：`Authorizer *Authorizer`、`AllowedHosts []string`、`AllowPrivateHosts bool`
  - 授权拦截时在 timeline 追加 `Kind == "recovery"`、`Detail == "authorization_blocked"` 事件，并把被拦截块的 `Rejection` 设为授权原因、`Approved` 置为 false。

**说明：** 授权门放在 `Preflight` 循环之后、计算 `approved` 之前。被授权拦截的块沿用现有"未批准块"通路：仍进入 `executor.Execute`（记录 evidence、状态为 `preflight_rejected`），永不真正运行，并通过 `renderPreflightRejections` 把授权原因回灌给模型。scope 在每轮用 `session.Target.Canonical` 的主机构造。

- [ ] **Step 1: Write the failing test**

追加到 `tests/_packages/internal/runtime/runner_test.go`：

```go
func TestRunnerBlocksOutOfScopeCodeBeforeExecution(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{
		{Content: "外联测试。\n```python\nimport requests\nrequests.get('https://evil.com/steal')\n```"},
		{Content: "```python\nimport requests\nrequests.get('https://example.com/status')\nprint('ok')\n```"},
		{Content: "TASK_COMPLETE"},
	}}
	executor := &recordingExecutor{results: []ExecutionResult{{
		Block: CodeBlock{Index: 1, Language: LanguagePython}, Status: ExecutionSucceeded, Stdout: "ok\n",
	}}}
	config := defaultRunnerConfig()
	config.Authorizer = NewAuthorizer(false)
	config.AllowPrivateHosts = true
	runner := NewRunner(client, executor, config, nil, nil)
	session := NewSession(Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Status != SessionDone {
		t.Fatalf("session = %+v", session)
	}
	blocked := false
	for _, event := range session.Timeline {
		if event.Kind == "recovery" && event.Detail == "authorization_blocked" {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("expected authorization_blocked timeline event: %+v", session.Timeline)
	}
	if len(client.requests) < 2 {
		t.Fatalf("request count = %d", len(client.requests))
	}
	if !containsMessage(client.requests[1].Messages, "user", "out of authorized scope") {
		t.Fatalf("scope rejection not fed back: %+v", client.requests[1].Messages)
	}
}

func TestRunnerBlocksDestructiveCodeBeforeExecution(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{
		{Content: "```bash\nrm -rf /tmp/loot\n```"},
		{Content: "```python\nimport requests\nrequests.get('https://example.com/status')\nprint('ok')\n```"},
		{Content: "TASK_COMPLETE"},
	}}
	executor := &recordingExecutor{results: []ExecutionResult{{
		Block: CodeBlock{Index: 1, Language: LanguagePython}, Status: ExecutionSucceeded, Stdout: "ok\n",
	}}}
	config := defaultRunnerConfig()
	config.Authorizer = NewAuthorizer(false)
	runner := NewRunner(client, executor, config, nil, nil)
	session := NewSession(Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if !containsMessage(client.requests[1].Messages, "user", "destructive") {
		t.Fatalf("destructive rejection not fed back: %+v", client.requests[1].Messages)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime -run 'TestRunnerBlocks' -count=1`
Expected: FAIL（`RunnerConfig` 无 `Authorizer`/`AllowPrivateHosts` 字段，且授权门未接入，越界代码会被执行、无 `authorization_blocked` 事件）。

- [ ] **Step 3: Write minimal implementation**

在 `internal/runtime/runner.go` 的 `RunnerConfig` 结构体追加字段（`OnEvent` 之后）：

```go
	Authorizer        *Authorizer
	AllowedHosts      []string
	AllowPrivateHosts bool
```

在 `Run` 方法内，把现有 preflight 循环（当前为）：

```go
		preflight := make([]PreflightResult, 0, len(blocks))
		approved := make([]PreflightResult, 0, len(blocks))
		for _, block := range blocks {
			result := Preflight(block)
			preflight = append(preflight, result)
			if result.Approved {
				approved = append(approved, result)
			}
		}
```

替换为（加入授权门；`scope` 每轮由目标主机构造）：

```go
		scope := NewScope(hostOf(session.Target.Canonical), runner.config.AllowedHosts, runner.config.AllowPrivateHosts)
		preflight := make([]PreflightResult, 0, len(blocks))
		approved := make([]PreflightResult, 0, len(blocks))
		authorizationBlocked := false
		for _, block := range blocks {
			result := Preflight(block)
			if result.Approved {
				if decision := runner.config.Authorizer.Authorize(result.Block, scope); !decision.Allowed {
					result.Approved = false
					result.Rejection = decision.Reason
					authorizationBlocked = true
				}
			}
			preflight = append(preflight, result)
			if result.Approved {
				approved = append(approved, result)
			}
		}
		if authorizationBlocked {
			session.AddEvent(turn, "recovery", "authorization_blocked", time.Now().UTC())
		}
```

在文件末尾（`assistantSummary` 之后）新增主机解析辅助函数：

```go
func hostOf(canonical string) string {
	parsed, err := url.Parse(canonical)
	if err != nil {
		return canonical
	}
	return parsed.Hostname()
}
```

在 `runner.go` 的 import 块加入 `"net/url"`（与现有 `regexp`、`strings` 等并列）。

说明：`renderPreflightRejections` 已按块打印 `reason:`，授权原因会随该文本一并回灌，无需改动它。被授权拦截的块保持 `Approved=false`，走现有 `len(approved) == 0` 或部分批准分支——两分支都调用 `executor.Execute(... Blocks: preflight)` 记录 evidence，未批准块在 `executeBlock` 中因 `!preflight.Approved` 直接返回 `ExecutionPreflightRejected`，不会真正执行。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/runtime -run 'TestRunnerBlocks' -count=1`
Expected: PASS

再跑整包回归：

Run: `go test ./internal/runtime -count=1`
Expected: PASS（现有测试不受影响；未配置 `Authorizer` 时 `nil` 授权器放行）。

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/runtime/runner.go tests/_packages/internal/runtime/runner_test.go
git add internal/runtime/runner.go tests/_packages/internal/runtime/runner_test.go
git commit -m "feat: enforce authorization gate in agent loop"
```

---

### Task 5: 应用层组装、系统提示词、文档与全量验证

**Files:**
- Modify: `internal/app/engagement.go`
- Modify: `internal/runtime/prompt.go`
- Modify: `README.md`
- Test: `tests/_packages/internal/app/engagement_test.go` (已存在，追加测试)

**Interfaces:**
- Consumes: `AuthorizationConfig`（Task 1）、`RunnerConfig.Authorizer/AllowedHosts/AllowPrivateHosts`（Task 4）。

- [ ] **Step 1: Write the failing test**

追加到 `tests/_packages/internal/app/engagement_test.go`（该测试锁定"授权配置被传入 Runner 时越界代码不执行"的端到端行为；沿用该文件已有的 fake client / 目录约定，如已有辅助不同请按文件现状对齐构造方式）：

```go
func TestServiceWiresAuthorizationFromConfig(t *testing.T) {
	cfg := config.Default()
	if !cfg.Agent.Authorization.IsEnabled() {
		t.Fatal("precondition: authorization enabled by default")
	}
	// 越界外联 + 随后 TASK_COMPLETE；断言 evidence 中越界块状态为 preflight_rejected。
	responses := []agent.Response{
		{Content: "```python\nimport requests\nrequests.get('https://evil.example.net/x')\n```"},
		{Content: "TASK_COMPLETE"},
	}
	dir := t.TempDir()
	service := NewService(cfg, Dependencies{
		NewAgentClient: func(config.AgentConfig) (agent.Client, error) {
			return &sequenceClient{responses: responses}, nil
		},
	})
	result, err := service.Run(context.Background(), Request{
		Target:     runtime.Target{Canonical: "https://example.com", Raw: "example.com"},
		Intent:     "检查目标",
		OutputRoot: dir,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.Status != runtime.SessionDone {
		t.Fatalf("session = %+v", result.Session)
	}
	blocked := false
	for _, event := range result.Session.Timeline {
		if event.Kind == "recovery" && event.Detail == "authorization_blocked" {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("expected authorization_blocked event: %+v", result.Session.Timeline)
	}
}
```

若 `engagement_test.go` 尚无名为 `sequenceClient` 的顺序返回假 client，则在该测试文件内新增最小实现：

```go
type sequenceClient struct {
	responses []agent.Response
	index     int
}

func (c *sequenceClient) Chat(context.Context, agent.Request) (agent.Response, error) {
	if c.index >= len(c.responses) {
		return agent.Response{Content: "TASK_COMPLETE"}, nil
	}
	response := c.responses[c.index]
	c.index++
	return response, nil
}
```

（若文件已有等价假 client，请复用它，不要重复定义，避免符号冲突。）

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app -run 'TestServiceWiresAuthorization' -count=1`
Expected: FAIL（`Service.Run` 尚未把 `Authorizer` 传入 `RunnerConfig`，越界块被执行且无 `authorization_blocked` 事件）。

- [ ] **Step 3: Write minimal implementation**

在 `internal/app/engagement.go` 的 `runner := runtime.NewRunner(...)` 的 `RunnerConfig` 字面量中，`OnEvent` 字段之后追加：

```go
		Authorizer:        authorizerFromConfig(agentConfig.Authorization),
		AllowedHosts:      agentConfig.Authorization.AllowedHosts,
		AllowPrivateHosts: agentConfig.Authorization.PrivateAllowed(),
```

在 `engagement.go` 文件末尾（`newEngagementID` 之后）新增：

```go
func authorizerFromConfig(auth config.AuthorizationConfig) *runtime.Authorizer {
	if !auth.IsEnabled() {
		return nil
	}
	return runtime.NewAuthorizer(auth.AllowDestructive)
}
```

在 `internal/runtime/prompt.go` 的 `systemPrompt` 末尾（`TASK_COMPLETE` 说明之后）追加一句，让模型知道范围约束：

```
Only target the authorized host and its subdomains. Do not contact unrelated external hosts and do not perform destructive write operations (INSERT/UPDATE/DELETE/DROP or destructive shell commands); such blocks are rejected before execution.
```

在 `README.md` 的「配置」章节 `agent` JSON 示例中，`anthropic` 对象之后加入 `authorization` 字段，并补一段说明；示例键：

```json
    "authorization": {
      "enabled": true,
      "allow_destructive": false,
      "allow_private_hosts": true,
      "allowed_hosts": []
    }
```

README 说明文字要点（新增一小段）：授权门在执行前校验，默认只允许目标主机及其子域、拦截破坏性 SQL/系统命令；`allowed_hosts` 追加额外授权主机；这是基于代码内 http(s) URL 的**静态**范围检查，非沙箱，模型仍可能用变量拼接绕过，属纵深防御一层。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app -run 'TestServiceWiresAuthorization' -count=1`
Expected: PASS

- [ ] **Step 5: 全量验证**

Run:

```bash
gofmt -w internal/app/engagement.go internal/runtime/prompt.go tests/_packages/internal/app/engagement_test.go
go build ./...
go test ./...
go test -race ./...
go vet ./...
go mod verify
git diff --check
```

Expected: 全部通过；`go build`/`go vet` 无输出即成功。

- [ ] **Step 6: Commit**

```bash
git add internal/app/engagement.go internal/runtime/prompt.go README.md tests/_packages/internal/app/engagement_test.go
git commit -m "feat: wire authorization scope engine into engagement"
```

---

## 自查

- **Spec 覆盖**：破坏性操作拦截（Task 3 SQL/Shell 正则）✓；范围强制（Task 2 Scope + Task 3 host 提取 + Task 4 接入）✓；安全默认（Task 1 `*bool` 访问器）✓；关闭时零行为变化（Task 3 nil 放行 + Task 4 nil 授权器）✓；模型可见约束（Task 5 系统提示词）✓；文档与限制声明（Task 5 README）✓。
- **占位符扫描**：无 TODO/TBD；每个代码步骤含完整代码与预期输出。
- **类型一致性**：`AuthorizationConfig`/`IsEnabled`/`PrivateAllowed`（Task 1）→ `authorizerFromConfig`（Task 5）；`NewScope`/`HostAllowed`（Task 2）→ Task 3/4 一致调用；`NewAuthorizer`/`Authorize`/`Decision`（Task 3）→ Task 4/5 一致；`RunnerConfig` 新字段（Task 4）→ Task 5 字面量一致；`hostOf`（Task 4）在 runner 内定义并使用。

## 集成前提（执行者须先确认）

1. `tests/_packages/internal/runtime/` 与 `internal/runtime/` 的测试文件为相对符号链接，新增测试必须先建真身再建链接（见各 Task）。
2. `engagement_test.go` 的既有假 client 名称以文件现状为准；Task 5 的 `sequenceClient` 仅在无等价实现时新增。
3. 本计划不改动 `Preflight`、执行器、报告的现有措辞与断言。
