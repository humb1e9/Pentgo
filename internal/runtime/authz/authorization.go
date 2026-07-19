package authz

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"pentgo/internal/runtime/exec"
)

// destructiveSQL 匹配会写入或破坏数据的 SQL 操作。
var destructiveSQL = []*regexp.Regexp{
	regexp.MustCompile("(?i)\\bINSERT\\s+(INTO|IGNORE)\\b"),
	regexp.MustCompile("(?i)\\bUPDATE\\s+\\w+\\s+SET\\b"),
	regexp.MustCompile("(?i)\\bDELETE\\s+FROM\\b"),
	regexp.MustCompile("(?i)\\bDROP\\s+(TABLE|DATABASE|INDEX|VIEW|PROCEDURE|SCHEMA)\\b"),
	regexp.MustCompile("(?i)\\bTRUNCATE\\s+(TABLE\\s+)?\\w+"),
	regexp.MustCompile("(?i)\\bCREATE\\s+(TABLE|DATABASE|USER|SCHEMA)\\b"),
	regexp.MustCompile("(?i)\\bALTER\\s+(TABLE|USER|DATABASE)\\b"),
	regexp.MustCompile("(?i)\\b(GRANT|REVOKE)\\s+\\w+\\s+ON\\b"),
	regexp.MustCompile("(?i)\\bRENAME\\s+TABLE\\b"),
	regexp.MustCompile("(?i)\\bREPLACE\\s+INTO\\b"),
}

// destructiveShell 匹配会破坏本地或远端系统的命令。
var destructiveShell = []*regexp.Regexp{
	regexp.MustCompile("(?i)\\brm\\s+-[a-z]*r[a-z]*f"),
	regexp.MustCompile("(?i)\\brm\\s+-[a-z]*f[a-z]*r"),
	regexp.MustCompile("(?i)\\bmkfs\\b"),
	regexp.MustCompile("(?i)\\bdd\\s+.*of=/dev/"),
	regexp.MustCompile("(?i)>\\s*/dev/sd[a-z]"),
	regexp.MustCompile("(?i)\\b(shutdown|reboot|halt|poweroff)\\b"),
	regexp.MustCompile(":\\(\\)\\s*\\{\\s*:\\|:&\\s*\\}"),
}

var urlPattern = regexp.MustCompile("(?i)https?://[^\\s'\"<>]+")

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
func (a *Authorizer) Authorize(block exec.CodeBlock, scope Scope) Decision {
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
