---
name: advsec-plus
description: >
  高级渗透测试补充技能 v1.0.1。覆盖 SecSkills-main 未涵盖的现代安全领域：
  API安全(JWT/OAuth/WebSocket)、移动端(Android/iOS/Frida)、
  云安全(元数据/容器/K8s)、供应链(Git泄露/依赖混淆/CI-CD)、
  现代Web漏洞(原型污染/2FA绕过/缓存欺骗)、高级规避(EDR/AMSI/Syscall)。
  所有漏洞输出必须附带可复现的验证请求（完整curl命令），用户需在Burp中自行验证。
  未核实的漏洞禁止作为正式漏洞输出。硬编码/未授权/敏感信息必须输出完整路径+行号。
  Git泄露/密钥泄露必须输出文件路径+行号+泄露值。
  当目标涉及API接口、移动App、云环境、JWT/OAuth、容器/K8s时触发。
  不触发: 传统Web漏洞(SQLi/XSS/RCE/SSRF等)，这些路由到 SecSkills-main。
allowed-tools: Read, Write, Bash, Grep, WebSearch, WebFetch, Glob, AskUserQuestion
argument-hint: <target>
---

# AdvSec-Plus — 高级渗透测试补充技能

> **定位**: SecSkills-main 的互补技能。SecSkills 覆盖传统 Web/主机/后渗透，
> 本技能覆盖 6 个现代安全领域：API安全 / 移动端 / 云安全 / 供应链 / 现代Web / 高级规避。
> **架构**: SKILL.md (L1) → references/ (L2) — 6 个专项文件

## 触发规则

**触发（任一满足）**:
1. 目标为 REST/GraphQL/gRPC API、WebSocket 接口
2. 涉及 JWT 令牌 / OAuth 2.0 / OIDC 协议
3. 涉及移动 App（APK/IPA）、Android/iOS 渗透、Frida/Objection
4. 云环境: AWS/Azure/GCP/阿里云/腾讯云 元数据、S3/OSS Blob、容器、K8s
5. Git 泄露、硬编码密钥、依赖混淆、CI/CD 管道安全
6. 原型污染 (Prototype Pollution)、2FA 绕过、Web 缓存欺骗
7. EDR 规避、AMSI Bypass、Syscall 直接调用、ETW Patch

**不触发（任一命中）**:
- SQL注入/XSS/RCE/SSRF/文件上传/LFI/XXE/反序列化 → **SecSkills-main**
- 传统端口扫描/目录爆破 → **SecSkills-main**
- Linux/Windows 提权/域渗透 → **SecSkills-main**
- "什么是JWT"、"云安全基础" → 普通问答

## 行为准则

1. ❗ **引用强制** — Payload/命令必须引用 `references/` 中文件章节。未覆盖 → `⚠️ UNABLE TO CITE`
2. ❗ **风险标注** — 漏洞标 🔴高危 / 🟡中危 / 🟢低危 + 利用条件
3. ❗ **链式思维** — 优先输出利用链，非孤立漏洞
4. ❗ **命令可执行** — 所有命令完整可复制，IP/端口/域名用 `<target>` 占位
5. ❗ **工具优先 Python** — 自定义 PoC/EXP 优先 Python 单文件

## ⚠️ 漏洞验证与输出强制规范

> 由 skill-evolver 定义，本技能所有漏洞输出必须遵守。

### 🔴 规范一：禁止输出未核实漏洞

| 情况 | 正确做法 |
|------|---------|
| 检测到"疑似"JWT攻击但未验证 | 输出验证请求 + 标注 `❓ 待验证` |
| 发现疑似Git泄露但未确认 | 输出完整验证命令 + 待确认 |
| 工具扫描出Bucket开放但未验证 | 手动验证后再输出 |
| 任何未确认的漏洞 | 一律输出 `❓ 待验证:` 模板 |

**未核实漏洞输出模板**:
```
❓ 待验证: [漏洞类型]
   检测依据: [检测命令/工具输出]
   验证请求: (请复制到Burp Repeater验证)
   curl -sk 'https://<target>/path' [完整Headers/Body]
   预期成功特征: [响应包含XXX表示漏洞存在]
   ⚠️ 此漏洞尚未验证，请你在Burp中确认后告知我
```

### 🔴 规范二：漏洞必须附带可复现的验证请求

每个漏洞输出格式:
```
📦 [漏洞标题]
   ├─ 类型: [分类]
   ├─ 风险: [🔴/🟡/🟢]
   ├─ 位置: [完整URL/文件路径]
   ├─ 验证请求: (复制到Burp Repeater)
   │   curl -sk 'https://<target>/vuln' -H '...' -d '...'
   ├─ 关键响应特征:
   │   [预期响应内容]
   ├─ 利用条件: [如果有]
   └─ 状态: ✅ 已验证 / ❓ 待你验证
```

### 🔴 规范三：敏感信息泄露必须输出完整路径+位置

| 漏洞子类型 | 必须包含的信息 |
|-----------|--------------|
| JWT Token泄露 | 完整URL + Token值(脱敏) + 解码后的Payload |
| API Key/密钥硬编码 | 文件完整URL + **行号** + 前后3行代码 |
| Git泄露 | 仓库完整URL + 恢复的文件列表 + 关键内容 |
| Bucket开放/S3权限 | Bucket完整URL + 可访问的文件列表 |
| 云元数据泄露 | 完整URL + 返回的敏感字段 |
| CI/CD密钥泄露 | 文件路径 + 行号 + 泄露的变量名(值脱敏) |

**敏感信息泄露专用模板**:
```
📦 敏感信息泄露
   ├─ 位置: https://<target>/config.js:42
   ├─ 泄露代码:
   │   40: window._env = {
   │   41:   API_BASE: "https://api.target.com",
   │   42:   API_SECRET: "sk-xxxxxxxxxxxxxxx",    ← 泄露
   │   43:   DB_PASS: "xxxxx"                     ← 泄露
   │   44: }
   ├─ 验证:
   │   curl -sk 'https://<target>/config.js'
   └─ 状态: ⚠️ 待你确认
```

### 🔴 规范四：禁止清单

| ❌ 禁止行为 | ✅ 正确做法 |
|-----------|-----------|
| "可能/疑似有JWT漏洞" | 输出验证请求 + 待验证 |
| "Git泄露可能"(不给路径) | 完整URL + 恢复命令 + 结果 |
| "Bucket可能公开"(不给验证) | aws s3 ls s3://bucket --no-sign-request |
| "密钥泄露"(不给位置) | 文件路径:行号 + 泄露值 |

## 幻觉防护

| 内容类型 | 正确 | 禁止 |
|---------|------|------|
| CVE 编号 | 引用 reference 或 `⚠️ UNABLE TO CITE` | 编造编号 |
| Payload/命令 | 从 `references/` 引用 | 凭记忆写 |
| 云元数据 IP | AWS/Azure/GCP 标准端点 | 自造 IP |
| 无匹配 | `⚠️ UNABLE TO ASSESS: 未覆盖，建议[行动]` | 凭经验断言 |

## 场景导航索引

### 1. API 安全测试
| 场景 | reference |
|------|----------|
| JWT 攻击 (alg none/弱密钥/kid注入/jku) | `references/api-security.md §1` |
| OAuth 2.0 / OIDC 配置错误 | `references/api-security.md §2` |
| REST API Fuzzing 方法论 | `references/api-security.md §3` |
| WebSocket 劫持 / CSWSH | `references/api-security.md §4` |
| gRPC 反射攻击 | `references/api-security.md §5` |
| API 速率限制绕过 | `references/api-security.md §6` |
| Swagger/OpenAPI 文档泄露 | `references/api-security.md §7` |
| GraphQL 批查询/深度递归 | `references/api-security.md §8` |

### 2. 移动端安全
| 场景 | reference |
|------|----------|
| Android APK 反编译+重打包 | `references/mobile-security.md §1` |
| Android 四大组件暴露 | `references/mobile-security.md §2` |
| Android WebView 漏洞 | `references/mobile-security.md §3` |
| ADB 调试接口利用 | `references/mobile-security.md §4` |
| iOS IPA 砸壳+分析 | `references/mobile-security.md §5` |
| Frida 动态注入基础 | `references/mobile-security.md §6` |
| Objection 运行时操作 | `references/mobile-security.md §7` |
| 证书固定绕过 | `references/mobile-security.md §8` |

### 3. 云安全
| 场景 | reference |
|------|----------|
| 云元数据服务利用 (AWS/Azure/GCP/阿里云) | `references/cloud-security.md §1` |
| 对象存储公开访问 (S3/OSS/Azure Blob) | `references/cloud-security.md §2` |
| Docker 容器逃逸 | `references/cloud-security.md §3` |
| Kubernetes 渗透 (API/Kubelet/Pod) | `references/cloud-security.md §4` |
| IAM 配置错误检测 | `references/cloud-security.md §5` |
| Serverless 安全测试 | `references/cloud-security.md §6` |

### 4. 供应链与开发安全
| 场景 | reference |
|------|----------|
| Git 泄露检测+利用 | `references/supply-chain-devsec.md §1` |
| 依赖混淆攻击 (Dep Confusion) | `references/supply-chain-devsec.md §2` |
| CI/CD 管道漏洞 (Actions/Jenkins) | `references/supply-chain-devsec.md §3` |
| 硬编码密钥/Token 检测 | `references/supply-chain-devsec.md §4` |
| Docker 镜像漏洞分析 | `references/supply-chain-devsec.md §5` |
| NPM/PyPI 投毒识别 | `references/supply-chain-devsec.md §6` |

### 5. 现代 Web 漏洞补充
| 场景 | reference |
|------|----------|
| 原型污染 (Prototype Pollution) | `references/web-advanced.md §1` |
| 2FA 绕过技术 | `references/web-advanced.md §2` |
| Web 缓存欺骗 (Cache Deception) | `references/web-advanced.md §3` |
| WebSocket 跨站劫持 | `references/web-advanced.md §4` |
| Service Worker 安全 | `references/web-advanced.md §5` |
| Import Map/HSTS 投毒 | `references/web-advanced.md §6` |

### 6. 高级规避 (红队)
| 场景 | reference |
|------|----------|
| AMSI Bypass (PowerShell) | `references/evasion-advanced.md §1` |
| ETW Patch (避让/禁用) | `references/evasion-advanced.md §2` |
| Syscall 直接调用 (Hell's Gate/Halos Gate) | `references/evasion-advanced.md §3` |
| EDR 用户态钩子检测+绕过 | `references/evasion-advanced.md §4` |
| 间接 syscall (DInvoke/SharpSploit) | `references/evasion-advanced.md §5` |
| .NET 程序集反射加载 | `references/evasion-advanced.md §6` |

## 工作流程

### Step 1: 目标分类
识别目标属于哪个领域: API / 移动端 / 云 / 供应链 / 现代Web / 高级规避
→ 按导航索引匹配 reference → 输出检视列表

### Step 2: 漏洞检测
按 area 加载对应 reference，每次 ≤1000 tokens
每条漏洞假设标注: 🔴高危/🟡中危/🟢低危 + 检测命令
引用格式: `[引用:references/xxx.md §N]`

### Step 3: 利用验证
从已加载的 reference 取 Payload → 可执行命令
优先构建利用链 (A→B→C→目标)
利用阶段只能引用攻击阶段已加载的文件

### Step 4: 报告输出
漏洞描述 → 技术分析 → 复现步骤 → PoC → 修复建议 → `[引用:file§N]`

## 零结果处理

| 情况 | 动作 |
|------|------|
| 目标不可达 | `❌ UNABLE TO ASSESS: 目标无响应` |
| Reference 未覆盖 | `⚠️ UNABLE TO CITE: 建议 WebSearch [关键词]` |
| 无授权 | 仅检测方法，不输出武器化链 |
| 利用失败 | 检查版本→防护→替代Payload |

## 路由边界

| 诉求 | 路由 |
|------|------|
| 传统 Web 漏洞 (SQLi/XSS/RCE/SSRF/文件上传等) | **SecSkills-main** |
| 主机渗透/提权/域渗透 | **SecSkills-main** |
| API/JWT/OAuth/WebSocket 安全 | **本 Skill** |
| 移动端 App 渗透 | **本 Skill** |
| 云安全/容器/K8s | **本 Skill** |
| Git泄露/依赖混淆/CI-CD | **本 Skill** |
| 原型污染/2FA绕过/缓存欺骗 | **本 Skill** |
| EDR规避/AMSI/Syscall | **本 Skill** |
| AI/LLM 安全测试 | secknowledge-skill |
| 查 CVE/技术文档 | WebSearch |

---

*v1.0.0 | AdvSec-Plus | 6 个领域 × references/ | 互补 SecSkills-main | 现代渗透测试补充*
