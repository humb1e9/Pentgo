---
name: api-unauth-fuzz
description: 前端接口未授权模糊测试技能。自动提取JS文件中的API路径，深度分析接口逻辑并智能构造参数，批量测试未授权访问和敏感信息泄露漏洞。新增Phase 2.5: 站点特征识别→技术栈探测→匹配公开已知路径字典→速率控制fuzz(10万条封顶)→输出HTTP 200+字节长度。所有漏洞输出必须附带可复现的curl验证命令，你在Burp中自行验证。未核实的漏洞禁止作为正式漏洞输出。JS硬编码/未授权敏感信息必须输出完整URL+行号。适用场景：Web应用安全测试、渗透测试、API安全审计。关键词：未授权访问、API测试、模糊测试、JS提取、敏感信息泄露、接口安全、路径fuzz、速率控制。
---

# 前端接口未授权模糊测试技能

## 触发条件
- 用户需要对Web应用进行API安全测试
- 用户提供了目标URL，需要测试未授权访问漏洞
- 用户需要从JS文件中提取API并智能构造参数测试
- 关键词：未授权、API测试、模糊测试、JS提取、敏感信息泄露

## 测试目标
- **未授权访问**: 无需认证即可访问的业务接口
- **敏感信息泄露**: 返回敏感数据（用户信息、配置信息、内部地址等）
- **越权访问**: 通过修改参数访问他人数据
- **IDOR漏洞**: 不安全的直接对象引用

## ⚠️ 漏洞验证与输出强制规范

> 由 skill-evolver 定义，本技能所有输出必须遵守。

### 🔴 规范一：禁止输出未核实漏洞
未在Burp中手动验证的漏洞禁止作为正式漏洞输出。发现疑似漏洞 → 输出验证请求让你测。

```
❓ 待验证: [接口路径]
   检测依据: [JS提取/参数fuzz]
   验证请求 (复制到Burp Repeater):
   curl -sk 'https://<target>/api/vuln' \
     -H 'Content-Type: application/json' \
     -d '{"page":1,"pageSize":20}'
   预期成功特征: 响应包含 data/list/total 等数据字段
   ⚠️ 请你用Burp验证后告知我结果
```

### 🔴 规范二：每个漏洞附带可复现验证请求

发现的每个未授权/敏感信息接口必须附带：

```
📦 [发现标题]
   ├─ 接口: POST /api/sensitive-data
   ├─ 完整URL: https://<target>/api/sensitive-data
   ├─ 验证请求:
   │   curl -sk 'https://<target>/api/sensitive-data' \
   │     -H 'Content-Type: application/json' \
   │     -d '{"page":1,"pageSize":20}'
   ├─ 关键响应:
   │   {"code":0,"data":[{"userId":1,"phone":"138***"}]}
   ├─ 泄露字段: userId, phone, email, idCard
   └─ 状态: ⚠️ 待你在Burp中验证
```

### 🔴 规范三：JS提取敏感信息必须输出完整路径+行号

从JS中发现硬编码密钥/Token/API地址时:

```
📦 JS敏感信息泄露
   ├─ 来源JS: https://<target>/static/js/chunk-vendors.js:284
   ├─ 泄露内容:
   │   282: const ACCESS_KEY = "LTAI5t..."
   │   283: const SECRET_KEY = "xxxxxxxxxxxxx"     ← 泄露
   │   284: const API_HOST = "http://192.168.1.100" ← 内网地址泄露
   ├─ 验证:
   │   curl -sk 'https://<target>/static/js/chunk-vendors.js' | sed -n '280,286p'
   └─ 状态: ⚠️ 待你确认
```

### 🔴 规范四：禁止清单

| ❌ 禁止 | ✅ 正确 |
|--------|--------|
| "发现未授权API"(不给请求) | 完整curl命令+Headers+Body |
| "JS找到密钥"(不给位置) | 完整URL+行号+泄露值 |
| "返回了敏感数据"(不给字段) | 具体字段名+响应片段 |
| "可能是漏洞"(不确定) | 输出验证请求，不报漏洞 |

## 完整测试流程

### Phase 0: 环境初始化

```bash
#!/bin/bash
# api_unauth_fuzz.sh - API未授权模糊测试完整脚本

set -e

# 配置参数
TARGET_URL="${1:-https://target.com}"
OUTPUT_DIR="./api_fuzz_$(date +%Y%m%d_%H%M%S)"
MAX_TIMEOUT=10
CONNECT_TIMEOUT=5
RETRY_COUNT=2

# 创建目录结构
mkdir -p "$OUTPUT_DIR"/{js,apis,responses,reports,evidence,fuzz_dicts}

# 日志函数
log_info()  { echo -e "\033[32m[*]\033[0m $1"; }
log_found() { echo -e "\033[33m[+]\033[0m $1"; }
log_vuln()  { echo -e "\033[31m[!]\033[0m $1"; }
log_warn()  { echo -e "\033[35m[?]\033[0m $1"; }
log_fuzz()  { echo -e "\033[36m[F]\033[0m $1"; }

echo "=========================================="
echo "  API未授权模糊测试工具 v3.0"
echo "=========================================="
log_info "目标: $TARGET_URL"
log_info "输出: $OUTPUT_DIR"
```

---

### Phase 1: 信息收集与JS提取

#### 1.1 探测前端入口页面

```bash
scan_entry_pages() {
  log_info "Phase 1.1: 探测前端入口页面..."
  
  local entry_pages=(
    "/" "/index.html" "/index.htm" "/login" "/signin"
    "/#/login" "/#/home" "/app" "/main" "/home"
    "/dashboard" "/admin" "/console" "/portal"
    "/web" "/h5" "/mobile" "/api-docs" "/swagger-ui.html"
    "/swagger-resources" "/v2/api-docs" "/v3/api-docs"
  )
  
  for page in "${entry_pages[@]}"; do
    local code=$(curl -sk -o /dev/null -w "%{http_code}" "${TARGET_URL}${page}" --max-time 5)
    if [[ "$code" =~ ^(200|301|302|304)$ ]]; then
      log_found "发现入口: $page (HTTP $code)"
      curl -sk "${TARGET_URL}${page}" > "${OUTPUT_DIR}/entry_$(echo $page | sed 's/[\/#]/_/g').html" 2>/dev/null
    fi
  done
}
```

#### 1.2 深度提取JS文件

```bash
extract_js_files() {
  log_info "Phase 1.2: 提取JS文件..."
  
  # 常见JS路径模式
  local js_patterns=(
    # 标准路径
    "/js/app.js" "/js/main.js" "/js/index.js" "/js/service.js"
    "/js/api.js" "/js/config.js" "/js/router.js" "/js/common.js"
    "/js/utils.js" "/js/request.js" "/js/http.js" "/js/ajax.js"
    
    # 框架特定
    "/static/js/app.js" "/static/js/main.js" "/static/js/chunk-vendors.js"
    "/dist/js/app.js" "/dist/js/main.js" "/dist/js/chunk-vendors.js"
    "/assets/js/app.js" "/assets/js/main.js"
    "/build/js/app.js" "/build/js/main.js"
    "/public/js/app.js" "/public/js/main.js"
    
    # Webpack/Vite打包
    "/bundle.js" "/bundle.min.js" "/app.js" "/app.min.js"
    "/main.js" "/main.min.js" "/index.js" "/index.min.js"
    "/chunk-vendors.js" "/vendors.js" "/runtime.js"
    
    # Angular特定
    "/scripts.js" "/inline.js" "/polyfills.js" "/styles.js"
    "/vendor.js" "/main.js" "/lazy.js"
    
    # React/Vue特定
    "/static/js/runtime-main.js" "/static/js/2.chunk.js"
    "/assets/index.js" "/assets/main.js"
  )
  
  local found_js=()
  
  # 探测常见JS路径
  for js in "${js_patterns[@]}"; do
    local code=$(curl -sk -o /dev/null -w "%{http_code}" "${TARGET_URL}${js}" --max-time 5)
    if [[ "$code" == "200" ]]; then
      log_found "发现JS: $js"
      found_js+=("$js")
      local filename=$(echo "$js" | sed 's/[\/\.]/_/g' | sed 's/^_//')
      curl -sk "${TARGET_URL}${js}" > "${OUTPUT_DIR}/js/${filename}.js" 2>/dev/null
    fi
  done
  
  # 从HTML中提取JS引用
  for html_file in "${OUTPUT_DIR}"/entry_*.html; do
    if [[ -f "$html_file" ]]; then
      # 提取src属性中的JS
      grep -oEi '(src|href)="[^"]+\.js[^"]*"' "$html_file" 2>/dev/null | \
        sed -E 's/(src|href)="//g' | sed 's/"//g' >> "${OUTPUT_DIR}/js_list.txt"
      
      # 提取动态加载的JS
      grep -oEi '\.load\(["'"'"'][^"'"'"']+\.js' "$html_file" 2>/dev/null | \
        sed -E "s/\.load\(['\"]//g" >> "${OUTPUT_DIR}/js_list.txt"
    fi
  done
  
  # 去重并下载
  if [[ -f "${OUTPUT_DIR}/js_list.txt" ]]; then
    sort -u "${OUTPUT_DIR}/js_list.txt" -o "${OUTPUT_DIR}/js_list.txt"
    
    while IFS= read -r js_path; do
      # 处理路径
      local url=""
      if [[ "$js_path" == http* ]]; then
        url="$js_path"
      elif [[ "$js_path" == //* ]]; then
        url="https:${js_path}"
      elif [[ "$js_path" == /* ]]; then
        url="${TARGET_URL}${js_path}"
      else
        url="${TARGET_URL}/${js_path}"
      fi
      
      local filename=$(echo "$js_path" | sed 's/[\/\.:?&=]/_/g' | sed 's/^_//')
      local size=$(curl -sk "$url" -o "${OUTPUT_DIR}/js/${filename}.js" -w "%{size_download}" 2>/dev/null)
      
      if [[ "$size" -gt 0 ]]; then
        log_found "下载JS: $js_path ($size bytes)"
      fi
    done < "${OUTPUT_DIR}/js_list.txt"
  fi
  
  log_info "JS文件总数: $(ls -1 "${OUTPUT_DIR}/js"/*.js 2>/dev/null | wc -l)"
}
```

---

### Phase 2: 深度API提取与分析

#### 2.1 多模式API路径提取

```bash
extract_api_paths() {
  log_info "Phase 2.1: 提取API路径..."
  
  local js_dir="${OUTPUT_DIR}/js"
  local api_file="${OUTPUT_DIR}/apis/all_raw_apis.txt"
  
  # 合并所有JS文件
  cat "$js_dir"/*.js 2>/dev/null > "$js_dir/all_merged.js"
  
  # 模式1: 标准URL路径 (单引号和双引号)
  grep -oE "'/[a-zA-Z0-9_/.-]+'" "$js_dir/all_merged.js" 2>/dev/null | tr -d "'" >> "$api_file"
  grep -oE '"/[a-zA-Z0-9_/.-]+"' "$js_dir/all_merged.js" 2>/dev/null | tr -d '"' >> "$api_file"
  
  # 模式2: 带路径参数的API (如 /user/:id, /api/{id})
  grep -oE "'/[a-zA-Z0-9_/.:{}-]+'" "$js_dir/all_merged.js" 2>/dev/null | tr -d "'" >> "$api_file"
  grep -oE '"/[a-zA-Z0-9_/.:{}-]+"' "$js_dir/all_merged.js" 2>/dev/null | tr -d '"' >> "$api_file"
  
  # 模式3: AngularJS路由定义 when('/path')
  grep -oE "when\(['\"][^'\"]+['\"]" "$js_dir/all_merged.js" 2>/dev/null | \
    sed -E "s/when\(['\"]//g" | sed "s/['\"]//g" >> "$api_file"
  
  # 模式4: Vue Router路径定义 path: '/path'
  grep -oE "path\s*:\s*['\"][^'\"]+['\"]" "$js_dir/all_merged.js" 2>/dev/null | \
    sed -E "s/path\s*:\s*['\"]//g" | sed "s/['\"]//g" >> "$api_file"
  
  # 模式5: fetch/axios/ajax请求URL
  grep -oE "(fetch|axios|ajax|http|request)\s*\(\s*['\"][^'\"]+['\"]" "$js_dir/all_merged.js" 2>/dev/null | \
    sed -E "s/(fetch|axios|ajax|http|request)\s*\(\s*['\"]//g" | sed "s/['\"]//g" >> "$api_file"
  
  # 模式6: 模板字符串中的URL
  grep -oE '`[^`]*`' "$js_dir/all_merged.js" 2>/dev/null | \
    grep -oE '/[a-zA-Z0-9_/.-]+' >> "$api_file"
  
  # 模式7: baseURL + url拼接模式
  grep -oE "(baseURL|baseUrl|BASE_URL)\s*[=:]\s*['\"][^'\"]+['\"]" "$js_dir/all_merged.js" 2>/dev/null | \
    sed -E "s/(baseURL|baseUrl|BASE_URL)\s*[=:]\s*['\"]//g" | sed "s/['\"]//g" > "${OUTPUT_DIR}/apis/base_urls.txt"
  
  # 模式8: API端点常量定义
  grep -oE "(API_URL|API_ENDPOINT|api\.)\s*['\"][^'\"]+['\"]" "$js_dir/all_merged.js" 2>/dev/null | \
    sed -E "s/(API_URL|API_ENDPOINT|api\.)\s*['\"]//g" | sed "s/['\"]//g" >> "$api_file"
  
  # 模式9: $http/$resource调用 (Angular)
  grep -oE '\$(http|resource)\.[a-z]+\(["'"'"'][^"'"'"']+["'"'"']' "$js_dir/all_merged.js" 2>/dev/null | \
    sed -E 's/\$(http|resource)\.[a-z]+\(["'"'"']//g' | sed "s/['\"]//g" >> "$api_file"
  
  # 去重排序
  sort -u "$api_file" -o "$api_file"
  
  # 过滤有效API路径
  grep -E '^/[a-zA-Z0-9_]' "$api_file" > "${OUTPUT_DIR}/apis/all_apis.txt" 2>/dev/null
  
  log_found "提取API总数: $(wc -l < "${OUTPUT_DIR}/apis/all_apis.txt")"
}
```

#### 2.2 深度分析API上下文

```bash
analyze_api_context() {
  log_info "Phase 2.2: 分析API上下文..."
  
  local js_file="${OUTPUT_DIR}/js/all_merged.js"
  local api_list="${OUTPUT_DIR}/apis/all_apis.txt"
  local analysis_dir="${OUTPUT_DIR}/apis/analysis"
  
  mkdir -p "$analysis_dir"
  
  while IFS= read -r api; do
    local safe_name=$(echo "$api" | sed 's/[\/]/_/g')
    local analysis_file="${analysis_dir}/${safe_name}.txt"
    
    # 获取API调用上下文 (前后30行)
    local context=$(grep -B 30 -A 30 -E "['\"]${api}['\"]|['\"]${api}/|${api}['\"]" "$js_file" 2>/dev/null | head -100)
    
    if [[ -n "$context" ]]; then
      echo "=== API: $api ===" > "$analysis_file"
      echo "" >> "$analysis_file"
      
      # 分析1: 提取请求方法
      local method=$(echo "$context" | grep -oEi '\.(get|post|put|delete|patch|head|options)\s*\(' | head -1 | sed 's/\.//g' | sed 's/(//g')
      echo "[Method] ${method:-UNKNOWN}" >> "$analysis_file"
      
      # 分析2: 提取参数定义
      echo "[Params Context]" >> "$analysis_file"
      echo "$context" | grep -E '(data|params|body|query|formData|requestData|payload)' | head -10 >> "$analysis_file"
      
      # 分析3: 提取JSON对象定义
      echo "" >> "$analysis_file"
      echo "[JSON Objects]" >> "$analysis_file"
      echo "$context" | grep -oE '\{[^}]{1,200}\}' | head -5 >> "$analysis_file"
      
      # 分析4: 提取变量赋值
      echo "" >> "$analysis_file"
      echo "[Variables]" >> "$analysis_file"
      echo "$context" | grep -oE '[a-zA-Z_][a-zA-Z0-9_]*\s*[:=]\s*[^,;}+]+' | head -10 >> "$analysis_file"
      
      # 分析5: 提取函数调用
      echo "" >> "$analysis_file"
      echo "[Function Calls]" >> "$analysis_file"
      echo "$context" | grep -oE '[a-zA-Z_][a-zA-Z0-9_]*\s*\([^)]*\)' | head -5 >> "$analysis_file"
    fi
  done < "$api_list"
  
  log_found "分析文件数: $(ls -1 "$analysis_dir"/*.txt 2>/dev/null | wc -l)"
}
```

#### 2.3 智能推断API类型和参数

```bash
infer_api_type() {
  local api=$1
  
  # API类型推断
  local api_type="unknown"
  local http_method="GET"
  local params='{}'
  
  # 根据路径关键词推断
  case "$api" in
    # 列表/查询类
    */list*|*/query*|*/search*|*/find*|*/get*|*/page*|*List*|*Query*|*Search*)
      api_type="list"
      http_method="POST"
      params='{"page":1,"pageSize":20}'
      ;;
    
    # 详情类
    */detail*|*/info*|*/view*|*/get/*|*Detail*|*Info*|*View*)
      api_type="detail"
      http_method="GET"
      params='{"id":1}'
      ;;
    
    # 添加/创建类
    */add*|*/create*|*/insert*|*/save*|*/new*|*Add*|*Create*|*Save*)
      api_type="create"
      http_method="POST"
      params='{"name":"test","status":1}'
      ;;
    
    # 更新/编辑类
    */update*|*/edit*|*/modify*|*/change*|*Update*|*Edit*|*Modify*)
      api_type="update"
      http_method="POST"
      params='{"id":1,"name":"test"}'
      ;;
    
    # 删除类
    */delete*|*/remove*|*/del*|*Delete*|*Remove*|*Del*)
      api_type="delete"
      http_method="POST"
      params='{"id":1}'
      ;;
    
    # 统计/报表类
    */count*|*/stat*|*/report*|*/chart*|*/echart*|*/dashboard*|*Count*|*Stat*|*Report*)
      api_type="stats"
      http_method="POST"
      params='{}'
      ;;
    
    # 配置类
    */config*|*/setting*|*/param*|*/option*|*Config*|*Setting*|*Param*)
      api_type="config"
      http_method="GET"
      params='{}'
      ;;
    
    # 字典类
    */dict*|*/enum*|*/type*|*Dict*|*Enum*)
      api_type="dict"
      http_method="POST"
      params='{}'
      ;;
    
    # 用户类
    */user*|*/member*|*/account*|*User*|*Member*)
      api_type="user"
      http_method="POST"
      params='{"page":1,"pageSize":20}'
      ;;
    
    # 组织/机构类
    */org*|*/unit*|*/department*|*/group*|*Org*|*Unit*|*Department*)
      api_type="org"
      http_method="POST"
      params='{"page":1,"pageSize":20}'
      ;;
    
    # 任务类
    */task*|*/job*|*/work*|*Task*|*Job*)
      api_type="task"
      http_method="POST"
      params='{"page":1,"pageSize":20}'
      ;;
    
    # 日志类
    */log*|*/history*|*/record*|*Log*|*History*)
      api_type="log"
      http_method="POST"
      params='{"page":1,"pageSize":20}'
      ;;
    
    # 文件类
    */file*|*/upload*|*/download*|*/export*|*File*|*Upload*)
      api_type="file"
      http_method="GET"
      params='{}'
      ;;
    
    # 权限类
    */role*|*/permission*|*/auth*|*Role*|*Permission*)
      api_type="auth"
      http_method="POST"
      params='{"page":1,"pageSize":20}'
      ;;
    
    # 账单/费用类
    */bill*|*/fee*|*/payment*|*Bill*|*Fee*)
      api_type="bill"
      http_method="POST"
      params='{"page":1,"pageSize":20}'
      ;;
    
    # 默认
    *)
      api_type="unknown"
      http_method="POST"
      params='{"page":1,"pageSize":20}'
      ;;
  esac
  
  echo "${api_type}|${http_method}|${params}"
}
```

---

### Phase 2.5: 站点特征识别与通用路径模糊测试

> 根据分析的站点特征(框架/技术栈)，匹配网上暴露的符合路径进行尝试与fuzz，
> 尽可能找出未授权访问。限制10万条路径，速率控制，输出200响应及字节长度。

#### 2.5.1 技术栈探测引擎

```bash
#==============================================================================
# 技术栈探测 - 从JS/HTML/Header中识别站点使用的框架和技术
#==============================================================================

detect_tech_stack() {
  log_info "Phase 2.5.1: 探测站点技术栈..."
  
  local html_dir="${OUTPUT_DIR}"
  local js_dir="${OUTPUT_DIR}/js"
  local merged_js="${js_dir}/all_merged.js"
  local tech_file="${OUTPUT_DIR}/fuzz_dicts/tech_stack.txt"
  
  > "$tech_file"
  
  local detected_techs=()
  
  # ========== 1. 从HTML Meta/Generator标签检测 ==========
  for html_file in "${OUTPUT_DIR}"/entry_*.html; do
    if [[ -f "$html_file" ]]; then
      # Vue.js
      if grep -qiE 'vue|Vue|vue\.js' "$html_file" 2>/dev/null; then
        detected_techs+=("Vue.js")
      fi
      # React
      if grep -qiE 'react|React|react\.js|react-dom' "$html_file" 2>/dev/null; then
        detected_techs+=("React")
      fi
      # Angular
      if grep -qiE 'angular|ng-|ng_app|ng-app' "$html_file" 2>/dev/null; then
        detected_techs+=("Angular")
      fi
      # jQuery
      if grep -qiE 'jquery|jQuery|jquery\.js' "$html_file" 2>/dev/null; then
        detected_techs+=("jQuery")
      fi
      # Bootstrap
      if grep -qiE 'bootstrap|bootstrap\.css|bootstrap\.js' "$html_file" 2>/dev/null; then
        detected_techs+=("Bootstrap")
      fi
      # 生成器
      local generator=$(grep -oEi '<meta[^>]*generator[^>]*content="([^"]+)"' "$html_file" 2>/dev/null | \
        sed -E 's/.*content="([^"]+)".*/\1/')
      if [[ -n "$generator" ]]; then
        detected_techs+=("Generator:$generator")
      fi
    fi
  done
  
  # ========== 2. 从JS文件内容检测 ==========
  if [[ -f "$merged_js" ]]; then
    # Vue.js
    if grep -qE 'createApp|Vue\.component|new Vue|defineComponent' "$merged_js" 2>/dev/null; then
      detected_techs+=("Vue.js(JS)")
    fi
    # React
    if grep -qE 'React\.createElement|useState|useEffect|createRoot' "$merged_js" 2>/dev/null; then
      detected_techs+=("React(JS)")
    fi
    # Angular
    if grep -qE 'Component\(|NgModule|Injectable|@angular' "$merged_js" 2>/dev/null; then
      detected_techs+=("Angular(JS)")
    fi
    # Axios
    if grep -qE 'axios\.|axios/' "$merged_js" 2>/dev/null; then
      detected_techs+=("Axios")
    fi
    # jQuery.ajax
    if grep -qE '\$\.ajax|\$\.get|\$\.post|jQuery\.ajax' "$merged_js" 2>/dev/null; then
      detected_techs+=("jQuery.Ajax")
    fi
    # Webpack
    if grep -qE 'webpackJsonp|__webpack_require__|webpackBootstrap' "$merged_js" 2>/dev/null; then
      detected_techs+=("Webpack")
    fi
    # Vite
    if grep -qE '__vite__|import\.meta\.env' "$merged_js" 2>/dev/null; then
      detected_techs+=("Vite")
    fi
    # Element UI
    if grep -qE 'el-|ElMessage|ElTable|element-ui|element/lib' "$merged_js" 2>/dev/null; then
      detected_techs+=("ElementUI")
    fi
    # Ant Design
    if grep -qE 'ant-|antd|@ant-design' "$merged_js" 2>/dev/null; then
      detected_techs+=("AntDesign")
    fi
    # ECharts
    if grep -qE 'echarts|ECharts|echarts\.init' "$merged_js" 2>/dev/null; then
      detected_techs+=("ECharts")
    fi
    # Moment.js
    if grep -qE 'moment\(|moment\.' "$merged_js" 2>/dev/null; then
      detected_techs+=("MomentJS")
    fi
  fi
  
  # ========== 3. 从API路径格式检测后端框架 ==========
  local api_list="${OUTPUT_DIR}/apis/all_apis.txt"
  if [[ -f "$api_list" ]]; then
    # Spring Boot (REST风格 /api/v1/xxx, /api/xxx/xxx)
    if grep -qE '/api/v[0-9]+/' "$api_list" 2>/dev/null; then
      detected_techs+=("SpringBoot(RESTful)")
    fi
    # Dubbo/HSF
    if grep -qE '\.(do|action)$' "$api_list" 2>/dev/null; then
      detected_techs+=("Struts/SpringMVC(.do)")
    fi
    # 阿里系
    if grep -qiE '/rest/|/gw/' "$api_list" 2>/dev/null; then
      detected_techs+=("AlibabaGateway")
    fi
    # .NET
    if grep -qE '\.(aspx|ashx|svc)$' "$api_list" 2>/dev/null; then
      detected_techs+=("ASP.NET")
    fi
    # PHP
    if grep -qE '\.php' "$api_list" 2>/dev/null; then
      detected_techs+=("PHP")
    fi
    # 通用REST
    if grep -qE '/api/' "$api_list" 2>/dev/null; then
      detected_techs+=("RESTfulAPI")
    fi
  fi
  
  # ========== 4. 从响应Header检测 ==========
  # 探测Server头
  local server_header=$(curl -sk -I "${TARGET_URL}" --max-time 5 2>/dev/null | grep -i '^server:' | head -1)
  if [[ -n "$server_header" ]]; then
    echo "[Server Header] $server_header" >> "$tech_file"
    if echo "$server_header" | grep -qiE 'nginx'; then
      detected_techs+=("Nginx")
    fi
    if echo "$server_header" | grep -qiE 'Apache'; then
      detected_techs+=("Apache")
    fi
    if echo "$server_header" | grep -qiE 'IIS|Microsoft-IIS'; then
      detected_techs+=("IIS")
    fi
    if echo "$server_header" | grep -qiE 'Tomcat|JBoss|WildFly'; then
      detected_techs+=("Tomcat/JBoss")
    fi
    if echo "$server_header" | grep -qiE 'Jetty'; then
      detected_techs+=("Jetty")
    fi
    if echo "$server_header" | grep -qiE 'cloudflare'; then
      detected_techs+=("Cloudflare")
    fi
  fi
  
  # 探测X-Powered-By
  local powered_by=$(curl -sk -I "${TARGET_URL}" --max-time 5 2>/dev/null | grep -i 'x-powered-by' | head -1)
  if [[ -n "$powered_by" ]]; then
    echo "[X-Powered-By] $powered_by" >> "$tech_file"
    if echo "$powered_by" | grep -qiE 'PHP|PHP/'; then
      detected_techs+=("PHP")
    fi
    if echo "$powered_by" | grep -qiE 'ASP\.NET|ASP\.NET'; then
      detected_techs+=("ASP.NET")
    fi
    if echo "$powered_by" | grep -qiE 'Express'; then
      detected_techs+=("Express(Node.js)")
    fi
    if echo "$powered_by" | grep -qiE 'Java|Servlet'; then
      detected_techs+=("Java/Servlet")
    fi
  fi
  
  # ========== 5. 从错误页面检测 ==========
  # 尝试触发错误
  local err_resp=$(curl -sk "${TARGET_URL}/nonexistent_path_12345" --max-time 5 2>/dev/null)
  if echo "$err_resp" | grep -qiE 'Spring|springframework|tomcat|java\.' 2>/dev/null; then
    detected_techs+=("Java/Spring")
  fi
  if echo "$err_resp" | grep -qiE 'ThinkPHP|Laravel|CodeIgniter|Yii|Phalcon' 2>/dev/null; then
    detected_techs+=("PHP-Framework")
  fi
  if echo "$err_resp" | grep -qiE 'ASP\.NET|\.NET|System\.Web' 2>/dev/null; then
    detected_techs+=("ASP.NET")
  fi
  if echo "$err_resp" | grep -qiE 'Node\.js|Express|connect\.' 2>/dev/null; then
    detected_techs+=("Node.js")
  fi
  if echo "$err_resp" | grep -qiE 'Django|Flask|Tornado' 2>/dev/null; then
    detected_techs+=("Python")
  fi
  if echo "$err_resp" | grep -qiE 'Ruby on Rails|sinatra|rack' 2>/dev/null; then
    detected_techs+=("Ruby")
  fi
  if echo "$err_resp" | grep -qiE 'Go|gin-gonic|beego' 2>/dev/null; then
    detected_techs+=("Go")
  fi
  
  # ========== 去重并输出 ==========
  local unique_techs=()
  for tech in "${detected_techs[@]}"; do
    local already=false
    for existing in "${unique_techs[@]}"; do
      [[ "$existing" == "$tech" ]] && already=true
    done
    $already || unique_techs+=("$tech")
  done
  
  # 写入文件
  printf '%s\n' "${unique_techs[@]}" > "$tech_file"
  
  log_found "识别技术栈: ${unique_techs[*]}"
  
  # 返回技术栈标识用于路径匹配
  echo "${unique_techs[*]}"
}
```

#### 2.5.2 基于技术栈的已知路径字典

```bash
#==============================================================================
# 根据识别的技术栈，加载匹配的已知路径字典
# 每个字典包含 公开已知的敏感路径、管理后台、调试接口、配置泄漏等
#==============================================================================

load_path_dict_by_tech() {
  local tech_stack=$1
  local dict_dir="${OUTPUT_DIR}/fuzz_dicts"
  local dict_file="${dict_dir}/targeted_paths.txt"
  
  > "$dict_file"
  
  # ========== 通用路径 (所有站点都测) ==========
  cat >> "$dict_file" << 'COMMON_PATHS'
# ===== 通用路径 (必测) =====
/admin
/administrator
/admin/login
/admin/index
/manager
/manage
/management
/console
/dashboard
/panel
/cpanel
/phpmyadmin
/pma
/myadmin
/adminer.php
/.env
/.git/config
/.git/HEAD
/.svn/entries
/.svn/wc.db
/.DS_Store
/backup
/backups
/bak
/bakup
/swagger-ui.html
/swagger-ui/
/swagger-resources
/v2/api-docs
/v3/api-docs
/swagger/v1/swagger.json
/api/swagger
/api-docs
/api/doc
/api/swagger.json
/*.swp
/*.swo
/config.json
/config.js
/configuration
/web.config
/appsettings.json
/Dockerfile
/docker-compose.yml
/nginx.conf
/.htaccess
/robots.txt
/sitemap.xml
/crossdomain.xml
/clientaccesspolicy.xml
/WS_FTP.LOG
/log
/logs
/error.log
/debug.log
/access.log
/tmp
/test
/tests
/dev
/debug
/api/debug
/api/test
/api/dev
/health
/healthcheck
/actuator/health
/actuator/info
/actuator
/actuator/env
/actuator/beans
/actuator/mappings
/actuator/httptrace
/actuator/heapdump
/actuator/threaddump
/actuator/loggers
/actuator/prometheus
/actuator/metrics
/actuator/configprops
/actuator/auditevents
/actuator/conditions
/actuator/scheduledtasks
/actuator/shutdown
/druid/index.html
/druid/login.html
/druid/sql.html
/druid/webapp.html
/druid/spring.html
/druid/api.json
/druid/websession.html
COMMON_PATHS

  # ========== 根据技术栈加载针对性路径 ==========
  
  # --- Vue.js 项目 ---
  if echo "$tech_stack" | grep -qiE 'Vue'; then
    cat >> "$dict_file" << 'VUE_PATHS'
# ===== Vue.js 特定路径 =====
/#/admin
/#/dashboard
/#/system
/#/user
/#/user/list
/#/user/add
/#/role
/#/permission
/#/config
/#/setting
/#/log
/#/login
/_nuxt/
/static/config.js
/static/constants.js
/static/env.js
VUE_PATHS
  fi

  # --- React 项目 ---
  if echo "$tech_stack" | grep -qiE 'React'; then
    cat >> "$dict_file" << 'REACT_PATHS'
# ===== React 特定路径 =====
/static/js/*.map
/service-worker.js
/precache-manifest.*.js
/asset-manifest.json
/manifest.json
/static/media/
/react/refresh
/_next/
/_next/static/
/next.config.js
/__nextjs_original-stack-frame
REACT_PATHS
  fi

  # --- Angular 项目 ---
  if echo "$tech_stack" | grep -qiE 'Angular'; then
    cat >> "$dict_file" << 'NG_PATHS'
# ===== Angular 特定路径 =====
/ngsw.json
/ngsw-worker.js
/safety-worker.js
/assets/config/
/assets/env.json
/assets/env.js
/environments/
/environments/environment.ts
/environments/environment.prod.ts
/environments/environment.dev.ts
/main.js.map
/polyfills.js.map
/runtime.js.map
NG_PATHS
  fi

  # --- Spring Boot / Java ---
  if echo "$tech_stack" | grep -qiE 'Spring|Java|Tomcat|JBoss'; then
    cat >> "$dict_file" << 'SPRING_PATHS'
# ===== Spring Boot / Java 特定路径 =====
/actuator/
/actuator/health
/actuator/info
/actuator/env
/actuator/beans
/actuator/mappings
/actuator/heapdump
/actuator/threaddump
/actuator/loggers
/actuator/prometheus
/actuator/metrics
/actuator/configprops
/actuator/gateway
/actuator/refresh
/actuator/restart
/actuator/scheduledtasks
/actuator/conditions
/actuator/auditevents
/actuator/caches
/actuator/httptrace
/actuator/integrationgraph
/actuator/quartz
/actuator/sessions
/actuator/shutdown
/actuator/spring-integration
/actuator/startup
/jolokia/
/jolokia/exec
/jolokia/list
/jolokia/version
/jolokia/search/**
/hystrix
/hystrix.stream
/hystrix-dashboard
/hystrix/monitor
/archaius
/config
/config/**
/eureka
/eureka/**
/zuul
/zuul/**
/refresh
/bus/refresh
/bus/env
/bus/**
/monitor
/prometheus
/metrics
/health
/info
/env
/beans
/mappings
/dump
/trace
/autoconfig
/configprops
/logfile
/loggers
/spring
/spring-web
/ribbon
/turbine.stream
/swagger-ui.html
/swagger-ui/**
/webjars/springfox-swagger-ui/
/v2/api-docs
/v3/api-docs
/swagger-resources
/druid/index.html
/druid/webapp.html
/druid/api.html
/druid/sql.html
/druid/websession.html
/druid/submitLogin
/druid/login.html
/template/
/templates/
/thymeleaf
/freemarker
/*.class
/*.jar
/WEB-INF/
/WEB-INF/web.xml
/WEB-INF/applicationContext.xml
/WEB-INF/classes/
/j_spring_security_logout
/j_spring_security_check
/spring-security-login
/spring-security
/sso/login
/sso/logout
/logout
/doLogout
SPRING_PATHS
  fi

  # --- Nginx ---
  if echo "$tech_stack" | grep -qiE 'Nginx'; then
    cat >> "$dict_file" << 'NGINX_PATHS'
# ===== Nginx 特定路径 =====
/nginx_status
/nginx.conf
/default.conf
/nginx-config
/health_status
/status
/nginx-health
/rtmp/stat
/rtmp/live/
/ws/
/web-socket
/.well-known/
/.well-known/acme-challenge/
/50x.html
/404.html
NGINX_PATHS
  fi

  # --- PHP (Laravel/ThinkPHP 等) ---
  if echo "$tech_stack" | grep -qiE 'PHP'; then
    cat >> "$dict_file" << 'PHP_PATHS'
# ===== PHP 特定路径 =====
/phpinfo.php
/info.php
/test.php
/php.php
/phpinfo
/index.php/admin
/index.php?route=common/home
/admin/index.php
/admin/login.php
/install.php
/upgrade.php
/config.php
/config.inc.php
/config.inc
/database.php
/db_config.php
/composer.json
/composer.lock
/.env.example
/storage/logs/laravel.log
/storage/logs/
/storage/framework/
/vendor/
/vendor/autoload.php
/artisan
/server.php
/.env.bak
/.env.save
/cache/
/runtime/
/data/
/upload/
/uploads/
/Public/
/Application/
/ThinkPHP/
/Laravel/
/artisan
/gii
/gii/
/debug/default
/debug/
/yii
/yii/
/index-test.php
/index_dev.php
/app_dev.php
/app_test.php
/config.php.bak
/config.dev.php
/var/log/
/session/
/temp/
PHP_PATHS
  fi

  # --- ASP.NET ---
  if echo "$tech_stack" | grep -qiE 'ASP\.NET|IIS'; then
    cat >> "$dict_file" << 'DOTNET_PATHS'
# ===== ASP.NET 特定路径 =====
/web.config
/Web.config
/App_Data/
/App_Code/
/Bin/
/obj/
/Packages/
/Trace.axd
/elmah.axd
/elmah
/elmah/
/Remote
/Remote/
/App_Browsers/
/App_LocalResources/
/App_GlobalResources/
/App_WebReferences/
/App_Themes/
/aspnet_client/
/aspnet-webadmin/
/aspnet-sysinfo/
/aspnet_compiler.exe
/iisstart.htm
/iis-86.png
/vti_bin/
/vti_inf.html
/vti_pvt/
/certenroll/
/certmscan/
/certsrv/
/certsrv/certfnsh.asp
/certsrv/certcarc.asp
/certsrv/certqok.asp
/ReportServer/
/Reports/
/ReportServer_Pages/
/MsmdService/
/OLAP/
/AnalysisServices/
/TeamFoundation/
/tfs/
/SiteServer/
/AdventureWorks/
/Northwind/
/Pubs/
/DWAdmin/
/WebAdmin/
/RemoteWebAdmin/
DOTNET_PATHS
  fi

  # --- 阿里云 / 云服务 ---
  if echo "$tech_stack" | grep -qiE 'Alibaba|Gateway|Cloudflare'; then
    cat >> "$dict_file" << 'CLOUD_PATHS'
# ===== 云服务/网关特定路径 =====
/gw/
/gw/api
/api-gateway/
/api-gateway/api
/apigateway/
/rest/api/
/rest/
/meta-data/
/latest/meta-data/
/latest/user-data/
/server-status
/server-info
/cgi-bin/
/cgi-bin/php
/cgi-bin/test.cgi
/fpm-status
/fpm-ping
CLOUD_PATHS
  fi

  # --- Webpack / Vite 构建工具 ---
  if echo "$tech_stack" | grep -qiE 'Webpack|Vite'; then
    cat >> "$dict_file" << 'BUILD_PATHS'
# ===== 构建工具特定路径 =====
/sourcemap/
/*.map
/*.js.map
/*.css.map
/__webpack_hmr
/__vite_ping
/ws
/webpack-dev-server
/sockjs-node
/sockjs-node/info
/hot-update.json
/*.hot-update.js
/*.hot-update.json
BUILD_PATHS
  fi

  # ========== 常用敏感文件路径 (补充) ==========
  cat >> "$dict_file" << 'EXTRA_SENSITIVE'
# ===== 扩展敏感路径 =====
/api/v1/admin
/api/v2/admin
/api/v3/admin
/api/admin
/api/private
/api/internal
/api/backup
/api/debug
/api/test
/api/mock
/api/docs
/api/graphql
/api/query
/api/monitor
/api/log
/api/logs
/api/config
/api/setting
/api/settings
/api/system
/api/sys
/api/permission
/api/role
/api/roles
/api/user
/api/users
/api/member
/api/members
/api/account
/api/accounts
/api/org
/api/orgs
/api/unit
/api/units
/api/department
/api/departments
/api/group
/api/groups
/api/file
/api/files
/api/upload
/api/download
/api/export
/api/import
/api/task
/api/tasks
/api/job
/api/jobs
/api/message
/api/messages
/api/notice
/api/notification
/api/notifications
/api/log
/api/logger
/api/audit
/api/auditlog
/api/operationlog
/api/dict
/api/dicts
/api/dictionary
/api/enum
/api/enums
/api/code
/api/codes
/api/cache
/api/redis
/api/mq
/api/rabbit
/api/kafka
/api/elasticsearch
/api/es
/api/search
/api/index
/api/indices
/api/sql
/api/query
/api/select
/api/insert
/api/update
/api/delete
/api/save
/api/add
/api/create
/api/edit
/api/modify
/api/remove
/api/del
/api/batch
/api/batchDelete
/api/exportExcel
/api/importExcel
/api/downloadExcel
/api/uploadFile
/api/downloadFile
/api/getAll
/api/getList
/api/getInfo
/api/getDetail
/api/selectAll
/api/selectList
/api/listAll
/api/findAll
/api/queryAll
/api/searchAll
/api/pageAll
/api/total
/api/count
/api/stat
/api/statistics
/api/report
/api/chart
/api/dashboard
/api/home
/api/index
/api/main
/api/swagger
/api/doc
/api/docs
/api/openapi
/api/open
/api/public
/api/anonymous
/api/noAuth
/api/withoutAuth
/backup/
/backup.sql
/backup.tar.gz
/backup.zip
/backup.rar
/backup.tar
/backup.sql.gz
/db.sql
/database.sql
/data.sql
/sql.sql
/mysql.sql
/init.sql
/schema.sql
/export.sql
/dump.sql
/db_backup.sql
/database_backup.sql
EXTRA_SENSITIVE

  # ========== 统计字典大小 ==========
  local total_lines=$(grep -vE '^#|^$' "$dict_file" | wc -l)
  log_found "基于技术栈生成路径字典: $total_lines 条"
  
  echo "$dict_file"
}
```

#### 2.5.3 速率控制的模糊测试引擎

```bash
#==============================================================================
# 速率控制的路径模糊测试引擎
# 限制: 最大10万条，速率: ~50-100 req/s，智能延迟
#==============================================================================

run_path_fuzz() {
  log_info "Phase 2.5.3: 开始通用路径模糊测试..."
  
  local dict_file=$1
  local rate_limit=${2:-80}  # 默认每秒80请求
  
  # 读取字典（去除注释和空行）
  local paths=()
  while IFS= read -r line; do
    # 跳过注释和空行
    [[ "$line" =~ ^#.*$ || -z "$line" ]] && continue
    paths+=("$line")
  done < "$dict_file"
  
  local total_paths=${#paths[@]}
  
  # 限制最大10万条
  if [[ "$total_paths" -gt 100000 ]]; then
    log_warn "路径数($total_paths)超过10万限制，随机抽取10万条"
    paths=($(shuf -e "${paths[@]}" | head -100000))
    total_paths=${#paths[@]}
  fi
  
  # 添加从JS中提取的API路径作为补充
  if [[ -f "${OUTPUT_DIR}/apis/all_apis.txt" ]]; then
    local api_count=0
    while IFS= read -r api_path; do
      [[ -z "$api_path" || "$api_path" =~ ^# ]] && continue
      # 不以通用路径开头且不在字典中的加入列表
      paths+=("$api_path")
      api_count=$((api_count + 1))
      # 最多补充500条API路径
      [[ "$api_count" -ge 500 ]] && break
    done < "${OUTPUT_DIR}/apis/all_apis.txt"
    total_paths=${#paths[@]}
  fi
  
  # 结果文件
  local fuzz_result="${OUTPUT_DIR}/fuzz_dicts/fuzz_results.txt"
  local fuzz_200="${OUTPUT_DIR}/fuzz_dicts/fuzz_200.txt"
  > "$fuzz_result"
  > "$fuzz_200"
  
  echo ""
  echo "=========================================="
  echo "  通用路径模糊测试"
  echo "  路径总数: $total_paths"
  echo "  速率限制: $rate_limit req/s"
  echo "=========================================="
  echo ""
  
  # ========== 速率控制参数 ==========
  # 目标: 不太快也不太慢
  # 每秒 rate_limit 个请求，带 ±20% 随机抖动
  # 每请求延迟 = (1000/rate_limit) ms，抖动范围 0.8x ~ 1.2x
  local base_delay=$(awk "BEGIN {printf \"%.2f\", 1000/$rate_limit}")
  local min_delay=$(awk "BEGIN {printf \"%.2f\", $base_delay * 0.8}")
  local max_delay=$(awk "BEGIN {printf \"%.2f\", $base_delay * 1.2}")
  
  log_info "基础延迟: ${base_delay}ms/请求 (±20% 抖动)"
  
  # ========== 并发控制 ==========
  # 使用5个并发槽，每个槽独立速率控制
  local concurrency=5
  local slot_delay=$(awk "BEGIN {printf \"%.2f\", $base_delay * $concurrency}")
  
  # ========== 统计变量 ==========
  local total=${#paths[@]}
  local current=0
  local count_200=0
  local count_3xx=0
  local count_401=0
  local count_403=0
  local count_404=0
  local count_500=0
  local count_other=0
  local count_error=0
  local last_report_time=$(date +%s)
  
  # 定义探测的HTTP方法（根据路径特征自动选择）
  # 对于已知路径，优先用GET，如果GET返回404则尝试POST
  
  # ========== 主循环：带速率控制的模糊测试 ==========
  local index=0
  for path in "${paths[@]}"; do
    index=$((index + 1))
    current=$((current + 1))
    
    # ===== 速率控制 =====
    # 每秒重置计数器，动态计算延迟
    local current_time=$(date +%s%N | cut -b1-13)
    local random_jitter=$(awk "BEGIN {srand(); printf \"%.2f\", $min_delay + rand() * ($max_delay - $min_delay)}")
    
    # ===== 请求前延迟（速率控制） =====
    # 每5个请求做一次速率校准
    if [[ $((index % concurrency)) -eq 0 ]]; then
      sleep $(awk "BEGIN {printf \"%.3f\", $random_jitter / 1000}")
    fi
    
    # ===== 进度显示 =====
    if [[ $((current % 50)) -eq 0 ]] || [[ "$current" -eq 1 ]] || [[ "$current" -eq "$total" ]]; then
      local elapsed=$(($(date +%s) - $(date +%s -d "now" 2>/dev/null || echo 0)))
      local rps=0
      [[ $elapsed -gt 0 ]] && rps=$((current / elapsed))
      printf "\r  [%6d/%6d] 200:%-4d 3xx:%-4d 401:%-4d 403:%-4d 404:%-4d 500:%-4d err:%-4d %d req/s  " \
        "$current" "$total" "$count_200" "$count_3xx" "$count_401" "$count_403" "$count_404" "$count_500" "$count_error" "$rps"
    fi
    
    # ===== 发送请求 =====
    local url="${TARGET_URL}${path}"
    
    # 处理特殊路径: 包含通配符*的用基础路径
    local test_path="$path"
    if echo "$test_path" | grep -q '\*'; then
      test_path=$(echo "$test_path" | sed 's/\*\.//g; s/\*/test/g')
      url="${TARGET_URL}${test_path}"
    fi
    
    # 判断方法: 含/api/或rest优先POST
    local method="GET"
    if echo "$test_path" | grep -qiE '/api/|/rest/|/gw/'; then
      method="POST"
    fi
    
    local start_time=$(date +%s%N)
    
    if [[ "$method" == "GET" ]]; then
      local resp=$(curl -sk -X GET "$url" \
        -H "Accept: application/json, text/plain, */*" \
        -H "User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36" \
        -H "Accept-Language: zh-CN,zh;q=0.9" \
        --connect-timeout 8 --max-time 15 \
        -w "\n%{http_code}\n%{size_download}" \
        -o /dev/stdout 2>/dev/null)
    else
      # POST请求 - 用空JSON body
      local resp=$(curl -sk -X POST "$url" \
        -H "Content-Type: application/json" \
        -H "Accept: application/json, text/plain, */*" \
        -H "User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36" \
        -H "Accept-Language: zh-CN,zh;q=0.9" \
        -d '{}' \
        --connect-timeout 8 --max-time 15 \
        -w "\n%{http_code}\n%{size_download}" \
        -o /dev/stdout 2>/dev/null)
    fi
    
    local end_time=$(date +%s%N)
    local req_time=$(( (end_time - start_time) / 1000000 ))  # ms
    
    # ===== 解析响应 =====
    local http_code=$(echo "$resp" | tail -2 | head -1)
    local body_size=$(echo "$resp" | tail -1)
    local body=$(echo "$resp" | sed '$d' | sed '$d')
    
    # 如果body_size不是数字，重新计算
    [[ ! "$body_size" =~ ^[0-9]+$ ]] && body_size=${#body}
    
    # ===== 分类记录 =====
    local result_line="${url}|${method}|${http_code}|${body_size}|${req_time}ms"
    echo "$result_line" >> "$fuzz_result"
    
    case "$http_code" in
      200|201|202|204)
        count_200=$((count_200 + 1))
        # ===== HTTP 200 - 记录详细信息（用户要求的） =====
        local resp_preview=$(echo "$body" | head -c 500)
        local is_real_content=false
        
        # 过滤：检查是否是真的内容（不是空白/错误页面）
        if [[ "$body_size" -gt 100 ]]; then
          # 检查是否包含有意义的内容
          if ! echo "$body" | grep -qiE '404 Not Found|404 Not Found|Cannot GET|Cannot POST|no such file|does not exist|Please login|请登录|未登录|没有权限|permission denied|forbidden|access denied'; then
            is_real_content=true
          fi
        fi
        
        if [[ "$is_real_content" == true ]]; then
          echo "${url}|${method}|${http_code}|${body_size}|${req_time}ms" >> "$fuzz_200"
          # 大内容标记
          local content_flag=""
          [[ "$body_size" -gt 10000 ]] && content_flag=" [大响应]"
          [[ "$body_size" -gt 100000 ]] && content_flag=" [超大响应!]"
          
          # 检查是否为JSON格式内容
          local json_flag=""
          if echo "$body" | grep -qE '^\s*(\{|\[)' 2>/dev/null; then
            json_flag=" [JSON]"
            # 检测是否存在敏感字段
            local sens=$(echo "$body" | grep -oE '"password"|"token"|"phone"|"email"|"idCard"|"secret"|"privateKey"|"accessToken"' | head -3 | tr '\n' ',' | sed 's/,$//')
            [[ -n "$sens" ]] && json_flag=" [JSON-敏感:${sens}]"
          fi
          
          log_found "[200] ${url} | 大小:${body_size}B${json_flag}${content_flag} | ${req_time}ms"
        else
          # 记录但标记可能误报
          echo "${url}|${method}|${http_code}|${body_size}|${req_time}ms|可能误报" >> "$fuzz_200"
        fi
        ;;
      301|302|303|307|308)
        count_3xx=$((count_3xx + 1))
        # 重定向路径也可疑，记录
        local redirect_to=$(echo "$body" | grep -oE 'Location: [^"]+' | head -1 || echo "$resp" | grep -oE '(?<=location href=")[^"]+' | head -1)
        if [[ -n "$redirect_to" ]]; then
          log_fuzz "[3xx] ${url} → ${redirect_to} (${body_size}B)"
        fi
        ;;
      401)
        count_401=$((count_401 + 1))
        ;;
      403)
        count_403=$((count_403 + 1))
        log_fuzz "[403] ${url} (${body_size}B) - 存在但被禁止"
        echo "${url}|${method}|${http_code}|${body_size}|${req_time}ms" >> "${OUTPUT_DIR}/fuzz_dicts/fuzz_403.txt"
        ;;
      404)
        count_404=$((count_404 + 1))
        ;;
      500|502|503)
        count_500=$((count_500 + 1))
        log_fuzz "[${http_code}] ${url} (${body_size}B) - 服务器错误"
        echo "${url}|${method}|${http_code}|${body_size}|${req_time}ms" >> "${OUTPUT_DIR}/fuzz_dicts/fuzz_5xx.txt"
        ;;
      *)
        count_other=$((count_other + 1))
        ;;
    esac
    
  done
  
  # ========== 输出统计 ==========
  echo ""
  echo ""
  echo "=========================================="
  echo "  模糊测试完成"
  echo "=========================================="
  log_found "总请求: $total"
  log_vuln "HTTP 200: $count_200 (已记录至 fuzz_200.txt)"
  log_info "HTTP 3xx: $count_3xx"
  log_info "HTTP 401: $count_401"
  log_info "HTTP 403: $count_403"
  log_info "HTTP 404: $count_404"
  log_info "HTTP 5xx: $count_500"
  log_info "其他: $count_other"
  log_info "错误: $count_error"
  
  # 输出200结果的摘要
  echo ""
  echo "=========================================="
  echo "  HTTP 200 结果明细 (路径 | 方法 | 状态 | 字节长度 | 耗时)"
  echo "=========================================="
  if [[ -s "$fuzz_200" ]]; then
    column -t -s'|' "$fuzz_200" 2>/dev/null || cat "$fuzz_200"
  else
    log_warn "未发现 HTTP 200 响应"
  fi
  
  echo ""
  log_found "详细结果: $fuzz_result"
  log_found "200结果: $fuzz_200"
  log_found "403结果: ${OUTPUT_DIR}/fuzz_dicts/fuzz_403.txt"
  log_found "5xx结果: ${OUTPUT_DIR}/fuzz_dicts/fuzz_5xx.txt"
}
```

#### 2.5.4 快速单路径批量验证 (如发现疑似路径后的二次验证)

```bash
#==============================================================================
# 快速批量验证 - 对发现的200/403路径进行多方法+多参数快速验证
#==============================================================================

quick_verify_findings() {
  log_info "Phase 2.5.4: 快速二次验证发现路径..."
  
  local fuzz_200="${OUTPUT_DIR}/fuzz_dicts/fuzz_200.txt"
  local verify_results="${OUTPUT_DIR}/fuzz_dicts/verify_results.txt"
  > "$verify_results"
  
  if [[ ! -s "$fuzz_200" ]]; then
    log_warn "没有发现需要二次验证的路径"
    return
  fi
  
  log_info "对发现的路径进行 GET/POST 双重验证..."
  
  while IFS='|' read -r url method code size time; do
    url=$(echo "$url" | xargs)
    
    # 提取路径
    local path="${url#${TARGET_URL}}"
    [[ -z "$path" ]] && path="$url"
    
    # 如果原请求是GET，尝试POST
    if [[ "$method" == "GET" ]]; then
      local post_resp=$(curl -sk -X POST "${TARGET_URL}${path}" \
        -H "Content-Type: application/json" \
        -H "Accept: application/json" \
        -H "User-Agent: Mozilla/5.0" \
        -d '{}' \
        --connect-timeout 8 --max-time 15 \
        -w "\n%{http_code}\n%{size_download}" \
        -o /dev/stdout 2>/dev/null)
      local post_code=$(echo "$post_resp" | tail -2 | head -1)
      local post_size=$(echo "$post_resp" | tail -1)
      if [[ "$post_code" == "200" || "$post_code" == "201" ]]; then
        log_found "[证实] ${path} 同时支持POST 200 (大小:${post_size}B)"
        echo "${path}|POST|${post_code}|${post_size}|证实POST同样可访问" >> "$verify_results"
      fi
    fi
    
    # 如果原请求是POST，尝试GET
    if [[ "$method" == "POST" ]]; then
      local get_resp=$(curl -sk -X GET "${TARGET_URL}${path}" \
        -H "Accept: application/json, text/plain, */*" \
        -H "User-Agent: Mozilla/5.0" \
        --connect-timeout 8 --max-time 15 \
        -w "\n%{http_code}\n%{size_download}" \
        -o /dev/stdout 2>/dev/null)
      local get_code=$(echo "$get_resp" | tail -2 | head -1)
      local get_size=$(echo "$get_resp" | tail -1)
      if [[ "$get_code" == "200" || "$get_code" == "201" ]]; then
        log_found "[证实] ${path} 同时支持GET 200 (大小:${get_size}B)"
        echo "${path}|GET|${get_code}|${get_size}|证实GET同样可访问" >> "$verify_results"
      fi
    fi
    
  done < "$fuzz_200"
}
```

### Phase 3: 智能参数构造引擎

#### 3.1 参数模板库

```bash
# 参数模板定义
declare -A PARAM_TEMPLATES

# 基础参数
PARAM_TEMPLATES[empty]='{}'
PARAM_TEMPLATES[pagination]='{"page":1,"pageSize":20,"pageNum":1,"current":1}'
PARAM_TEMPLATES[pagination_large]='{"page":1,"pageSize":1000,"pageNum":1}'
PARAM_TEMPLATES[id]='{"id":1}'
PARAM_TEMPLATES[id_string]='{"id":"1"}'
PARAM_TEMPLATES[ids]='{"ids":[1,2,3]}'

# 搜索参数
PARAM_TEMPLATES[search]='{"keyword":"","search":"","name":"","query":""}'
PARAM_TEMPLATES[search_with_page]='{"keyword":"","page":1,"pageSize":20}'

# 时间参数
PARAM_TEMPLATES[time]='{"startTime":"","endTime":""}'
PARAM_TEMPLATES[time_with_page]='{"startTime":"","endTime":"","page":1,"pageSize":20}'
PARAM_TEMPLATES[date]='{"startDate":"","endDate":""}'

# 状态参数
PARAM_TEMPLATES[status]='{"status":1}'
PARAM_TEMPLATES[status_with_page]='{"status":1,"page":1,"pageSize":20}'

# 用户相关
PARAM_TEMPLATES[user]='{"userId":1}'
PARAM_TEMPLATES[user_detail]='{"userId":1,"userName":"","phone":"","email":""}'
PARAM_TEMPLATES[user_page]='{"userId":1,"page":1,"pageSize":20}'

# 组织相关
PARAM_TEMPLATES[org]='{"unitId":1,"orgId":1}'
PARAM_TEMPLATES[org_page]='{"unitId":1,"page":1,"pageSize":20}'
PARAM_TEMPLATES[org_code]='{"corporateCode":"","orgCode":""}'

# 任务相关
PARAM_TEMPLATES[task]='{"taskId":1}'
PARAM_TEMPLATES[task_page]='{"taskId":1,"taskStatus":"","page":1,"pageSize":20}'

# 产品相关
PARAM_TEMPLATES[product]='{"productId":1}'
PARAM_TEMPLATES[product_page]='{"productId":1,"productName":"","page":1,"pageSize":20}'

# 角色权限
PARAM_TEMPLATES[role]='{"roleId":1}'
PARAM_TEMPLATES[role_page]='{"roleId":1,"roleName":"","page":1,"pageSize":20}'

# 字典配置
PARAM_TEMPLATES[dict]='{"dictType":"","dictCode":""}'
PARAM_TEMPLATES[dict_page]='{"dictType":"","page":1,"pageSize":20}'
```

#### 3.2 从JS上下文提取真实参数

```bash
extract_real_params() {
  local api=$1
  local analysis_file="${OUTPUT_DIR}/apis/analysis/$(echo $api | sed 's/[\/]/_/g').txt"
  local extracted_params=''
  
  if [[ -f "$analysis_file" ]]; then
    local context=$(cat "$analysis_file")
    
    # 方法1: 提取data/params/body等变量赋值
    local data_line=$(echo "$context" | grep -E 'data\s*[=:]\s*\{' | head -1)
    if [[ -n "$data_line" ]]; then
      local json_obj=$(echo "$data_line" | grep -oE '\{[^}]+\}')
      if [[ -n "$json_obj" ]]; then
        extracted_params="$json_obj"
      fi
    fi
    
    # 方法2: 提取函数调用中的参数
    if [[ -z "$extracted_params" ]]; then
      local func_call=$(echo "$context" | grep -E '\$(http|resource)\.(get|post|put|delete)' | head -1)
      if [[ -n "$func_call" ]]; then
        local json_obj=$(echo "$func_call" | grep -oE '\{[^}]+\}')
        if [[ -n "$json_obj" ]]; then
          extracted_params="$json_obj"
        fi
      fi
    fi
    
    # 方法3: 提取变量定义
    if [[ -z "$extracted_params" ]]; then
      local var_defs=$(echo "$context" | grep -E 'var|let|const' | grep -E '\{' | head -1)
      if [[ -n "$var_defs" ]]; then
        local json_obj=$(echo "$var_defs" | grep -oE '\{[^}]+\}')
        if [[ -n "$json_obj" ]]; then
          extracted_params="$json_obj"
        fi
      fi
    fi
    
    # 方法4: 提取请求配置对象
    if [[ -z "$extracted_params" ]]; then
      local req_config=$(echo "$context" | grep -A 5 -E 'params:|data:|body:' | grep -E '\{' | head -1)
      if [[ -n "$req_config" ]]; then
        local json_obj=$(echo "$req_config" | grep -oE '\{[^}]+\}')
        if [[ -n "$json_obj" ]]; then
          extracted_params="$json_obj"
        fi
      fi
    fi
  fi
  
  # 返回提取的参数或空
  echo "${extracted_params:-}"
}
```

#### 3.3 智能模糊猜想参数构造引擎 (核心)

```bash
#==============================================================================
# 智能模糊猜想参数构造引擎
# 当无法从JS上下文提取到真实参数时，启用智能猜想模式
#==============================================================================

# 参数字段名字典 - 根据业务场景智能匹配
declare -A FIELD_NAME_DICT

# 通用ID字段
FIELD_NAME_DICT[id]='id Id ID userId UserId userID user_id orgId OrgId org_id unitId UnitId unit_id'
FIELD_NAME_DICT[id_list]='ids Ids IDS userIds orgIds unitIds'

# 分页字段
FIELD_NAME_DICT[page]='page Page pageNum pageNum pageNo pageNumber current current_page'
FIELD_NAME_DICT[size]='pageSize PageSize size limit per_page perPage count'

# 名称字段
FIELD_NAME_DICT[name]='name Name userName user_name realName real_name trueName trueName nickname nick_name'
FIELD_NAME_DICT[title]='title Title subject Subject'

# 状态字段
FIELD_NAME_DICT[status]='status Status state State type Type'

# 时间字段
FIELD_NAME_DICT[time]='startTime endTime startDate endDate beginTime endTime createTime updateTime'
FIELD_NAME_DICT[date]='date Date year Year month Month day Day'

# 搜索字段
FIELD_NAME_DICT[search]='keyword keyword search query key words name code'

# 编码字段
FIELD_NAME_DICT[code]='code Code corporateCode orgCode unitCode productCode dictCode typeCode'

# 手机邮箱
FIELD_NAME_DICT[phone]='phone Phone mobile Mobile telephone tel cellphone'
FIELD_NAME_DICT[email]='email Email mail e_mail emailAddress'

# 其他常用字段
FIELD_NAME_DICT[desc]='description desc remark note comment content'
FIELD_NAME_DICT[address]='address addr location province city district street'

# 智能生成参数组合
smart_fuzz_params() {
  local api=$1
  local fuzz_params=()
  
  log_fuzz "启用智能模糊猜想模式: $api"
  
  # ==================== 策略1: 基础参数组合 ====================
  # 空参数
  fuzz_params+=('{}')
  
  # ==================== 策略2: 根据API路径关键词智能匹配 ====================
  local path_keywords=$(echo "$api" | tr '/' '\n' | grep -v '^$')
  
  # 2.1 检测ID相关关键词
  if echo "$api" | grep -qiE 'id|Id|ID'; then
    # 尝试多种ID命名变体
    for id_field in id Id ID userId orgId unitId taskId productId roleId; do
      fuzz_params+=("{\"$id_field\":1}")
      fuzz_params+=("{\"$id_field\":\"1\"}")
      fuzz_params+=("{\"$id_field\":1,\"page\":1,\"pageSize\":20}")
    done
  fi
  
  # 2.2 检测列表/查询关键词
  if echo "$api" | grep -qiE 'list|query|search|find|page|get.*s$'; then
    # 分页参数变体
    fuzz_params+=('{"page":1,"pageSize":20}')
    fuzz_params+=('{"pageNum":1,"pageSize":20}')
    fuzz_params+=('{"page":1,"size":20}')
    fuzz_params+=('{"current":1,"size":20}')
    fuzz_params+=('{"page":1,"pageSize":100}')
    fuzz_params+=('{"page":1,"pageSize":1000}')
    fuzz_params+=('{"page":1,"limit":20}')
    fuzz_params+=('{"offset":0,"limit":20}')
    
    # 带搜索条件
    fuzz_params+=('{"keyword":"","page":1,"pageSize":20}')
    fuzz_params+=('{"name":"","page":1,"pageSize":20}')
    fuzz_params+=('{"status":"","page":1,"pageSize":20}')
  fi
  
  # 2.3 检测详情关键词
  if echo "$api" | grep -qiE 'detail|info|view|get[^s]'; then
    for id_field in id Id ID; do
      fuzz_params+=("{\"$id_field\":1}")
      fuzz_params+=("{\"$id_field\":\"1\"}")
    done
  fi
  
  # 2.4 检测添加/创建关键词
  if echo "$api" | grep -qiE 'add|create|insert|save|new'; then
    fuzz_params+=('{"name":"test"}')
    fuzz_params+=('{"name":"test","status":1}')
    fuzz_params+=('{"title":"test"}')
    fuzz_params+=('{"code":"test"}')
  fi
  
  # 2.5 检测更新关键词
  if echo "$api" | grep -qiE 'update|edit|modify'; then
    fuzz_params+=('{"id":1,"name":"test"}')
    fuzz_params+=('{"id":1,"status":1}')
    fuzz_params+=('{"id":"1","name":"test"}')
  fi
  
  # 2.6 检测删除关键词
  if echo "$api" | grep -qiE 'delete|remove|del'; then
    for id_field in id Id ID ids Ids; do
      fuzz_params+=("{\"$id_field\":1}")
      fuzz_params+=("{\"$id_field\":\"1\"}")
      [[ "$id_field" == *"s" ]] && fuzz_params+=("{\"$id_field\":[1,2,3]}")
    done
  fi
  
  # ==================== 策略3: 业务领域关键词匹配 ====================
  
  # 3.1 用户相关
  if echo "$api" | grep -qiE 'user|member|account|customer'; then
    fuzz_params+=('{"userId":1}')
    fuzz_params+=('{"userId":1,"page":1,"pageSize":20}')
    fuzz_params+=('{"userName":"","page":1,"pageSize":20}')
    fuzz_params+=('{"phone":"","page":1,"pageSize":20}')
    fuzz_params+=('{"email":"","page":1,"pageSize":20}')
    fuzz_params+=('{"status":1,"page":1,"pageSize":20}')
    fuzz_params+=('{"userType":"","page":1,"pageSize":20}')
  fi
  
  # 3.2 组织/机构相关
  if echo "$api" | grep -qiE 'org|unit|department|group|branch'; then
    fuzz_params+=('{"unitId":1}')
    fuzz_params+=('{"orgId":1}')
    fuzz_params+=('{"unitId":1,"page":1,"pageSize":20}')
    fuzz_params+=('{"orgCode":"","page":1,"pageSize":20}')
    fuzz_params+=('{"corporateCode":"","page":1,"pageSize":20}')
    fuzz_params+=('{"unitName":"","page":1,"pageSize":20}')
    fuzz_params+=('{"parentId":1,"page":1,"pageSize":20}')
  fi
  
  # 3.3 任务相关
  if echo "$api" | grep -qiE 'task|job|work|process'; then
    fuzz_params+=('{"taskId":1}')
    fuzz_params+=('{"taskId":1,"page":1,"pageSize":20}')
    fuzz_params+=('{"taskStatus":"","page":1,"pageSize":20}')
    fuzz_params+=('{"taskType":"","page":1,"pageSize":20}')
    fuzz_params+=('{"status":1,"page":1,"pageSize":20}')
  fi
  
  # 3.4 产品相关
  if echo "$api" | grep -qiE 'product|goods|item'; then
    fuzz_params+=('{"productId":1}')
    fuzz_params+=('{"productId":1,"page":1,"pageSize":20}')
    fuzz_params+=('{"productName":"","page":1,"pageSize":20}')
    fuzz_params+=('{"productType":"","page":1,"pageSize":20}')
    fuzz_params+=('{"code":"","page":1,"pageSize":20}')
  fi
  
  # 3.5 角色/权限相关
  if echo "$api" | grep -qiE 'role|permission|auth|privilege'; then
    fuzz_params+=('{"roleId":1}')
    fuzz_params+=('{"roleId":1,"page":1,"pageSize":20}')
    fuzz_params+=('{"roleName":"","page":1,"pageSize":20}')
    fuzz_params+=('{"userId":1}')
  fi
  
  # 3.6 日志相关
  if echo "$api" | grep -qiE 'log|history|record|audit'; then
    fuzz_params+=('{"page":1,"pageSize":20}')
    fuzz_params+=('{"startTime":"","endTime":"","page":1,"pageSize":20}')
    fuzz_params+=('{"startDate":"","endDate":"","page":1,"pageSize":20}')
    fuzz_params+=('{"type":"","page":1,"pageSize":20}')
    fuzz_params+=('{"userId":1,"page":1,"pageSize":20}')
    fuzz_params+=('{"module":"","page":1,"pageSize":20}')
  fi
  
  # 3.7 字典/配置相关
  if echo "$api" | grep -qiE 'dict|enum|config|setting|param|option'; then
    fuzz_params+=('{}')
    fuzz_params+=('{"dictType":""}')
    fuzz_params+=('{"type":""}')
    fuzz_params+=('{"code":""}')
    fuzz_params+=('{"key":""}')
    fuzz_params+=('{"dictType":"","page":1,"pageSize":20}')
    fuzz_params+=('{"group":"","page":1,"pageSize":20}')
  fi
  
  # 3.8 文件相关
  if echo "$api" | grep -qiE 'file|upload|download|export|import|attachment'; then
    fuzz_params+=('{}')
    fuzz_params+=('{"id":1}')
    fuzz_params+=('{"fileId":1}')
    fuzz_params+=('{"fileName":""}')
    fuzz_params+=('{"type":""}')
  fi
  
  # 3.9 账单/费用相关
  if echo "$api" | grep -qiE 'bill|fee|payment|order|invoice'; then
    fuzz_params+=('{"page":1,"pageSize":20}')
    fuzz_params+=('{"month":"","page":1,"pageSize":20}')
    fuzz_params+=('{"year":"","page":1,"pageSize":20}')
    fuzz_params+=('{"status":"","page":1,"pageSize":20}')
    fuzz_params+=('{"unitId":1,"page":1,"pageSize":20}')
  fi
  
  # 3.10 消息/通知相关
  if echo "$api" | grep -qiE 'message|notice|notification|announcement'; then
    fuzz_params+=('{"page":1,"pageSize":20}')
    fuzz_params+=('{"status":"","page":1,"pageSize":20}')
    fuzz_params+=('{"type":"","page":1,"pageSize":20}')
    fuzz_params+=('{"isRead":"","page":1,"pageSize":20}')
  fi
  
  # ==================== 策略4: 路径参数提取与替换 ====================
  if echo "$api" | grep -qE ':[a-zA-Z_]+|\{[a-zA-Z_]+\}'; then
    # 提取路径参数名
    local path_params=$(echo "$api" | grep -oE ':[a-zA-Z_]+|\{[a-zA-Z_]+\}' | tr -d ':{}')
    
    for param in $path_params; do
      case "$param" in
        id|Id|ID)
          fuzz_params+=("{\"$param\":1}")
          fuzz_params+=("{\"$param\":\"1\"}")
          ;;
        userId|orgId|unitId|taskId|productId|roleId|fileId)
          fuzz_params+=("{\"$param\":1}")
          fuzz_params+=("{\"$param\":\"1\"}")
          ;;
        code|Code|code)
          fuzz_params+=("{\"$param\":\"test\"}")
          ;;
        name|Name)
          fuzz_params+=("{\"$param\":\"test\"}")
          ;;
        *)
          fuzz_params+=("{\"$param\":1}")
          fuzz_params+=("{\"$param\":\"\"}")
          ;;
      esac
    done
  fi
  
  # ==================== 策略5: 通用模糊测试参数 ====================
  # 这些参数适用于任何不确定的API
  
  # 5.1 最小参数集
  fuzz_params+=('{"page":1,"pageSize":20}')
  fuzz_params+=('{"id":1}')
  fuzz_params+=('{"status":1}')
  
  # 5.2 常用字段组合
  fuzz_params+=('{"name":"","page":1,"pageSize":20}')
  fuzz_params+=('{"code":"","page":1,"pageSize":20}')
  fuzz_params+=('{"type":"","page":1,"pageSize":20}')
  fuzz_params+=('{"status":"","page":1,"pageSize":20}')
  
  # 5.3 时间范围参数
  fuzz_params+=('{"startTime":"","endTime":""}')
  fuzz_params+=('{"startDate":"","endDate":""}')
  
  # 5.4 大数据量测试 (DoS检测)
  fuzz_params+=('{"page":1,"pageSize":10000}')
  fuzz_params+=('{"page":1,"pageSize":50000}')
  
  # 5.5 排序参数
  fuzz_params+=('{"page":1,"pageSize":20,"orderBy":""}')
  fuzz_params+=('{"page":1,"pageSize":20,"sort":"","order":"desc"}')
  
  # ==================== 策略6: 从API路径提取潜在字段名 ====================
  # 分析路径中的单词，生成可能的参数名
  local path_words=$(echo "$api" | tr '/_' '\n' | grep -v '^$' | grep -E '^[a-zA-Z]+$')
  
  for word in $path_words; do
    # 转换为驼峰命名
    local camel_case=$(echo "$word" | sed -E 's/(^|_)([a-z])/\U\2/g')
    local lower_case=$(echo "$word" | tr '[:upper:]' '[:lower:]')
    
    # 生成参数
    fuzz_params+=("{\"${lower_case}\":\"\"}")
    fuzz_params+=("{\"${lower_case}\":\"\",\"page\":1,\"pageSize\":20}")
    fuzz_params+=("{\"${lower_case}Id\":1}")
    fuzz_params+=("{\"${lower_case}Id\":1,\"page\":1,\"pageSize\":20}")
    fuzz_params+=("{\"${lower_case}Code\":\"\"}")
    fuzz_params+=("{\"${lower_case}Name\":\"\"}")
    fuzz_params+=("{\"${lower_case}Type\":\"\"}")
  done
  
  # ==================== 去重并返回 ====================
  printf '%s\n' "${fuzz_params[@]}" | sort -u
}

# 生成完整测试参数集 (整合JS提取 + 智能猜想)
generate_test_params() {
  local api=$1
  local params_list=()
  
  # 1. 空参数 (必须)
  params_list+=('{}')
  
  # 2. 尝试从JS上下文提取真实参数
  local real_params=$(extract_real_params "$api")
  if [[ -n "$real_params" && "$real_params" != "{}" ]]; then
    params_list+=("$real_params")
    log_found "从JS上下文提取参数: $real_params"
  else
    log_fuzz "JS上下文未找到参数，启用智能猜想模式"
  fi
  
  # 3. 根据API类型推断的参数
  local inference=$(infer_api_type "$api")
  local inferred_params=$(echo "$inference" | cut -d'|' -f3)
  if [[ -n "$inferred_params" && "$inferred_params" != "{}" ]]; then
    params_list+=("$inferred_params")
  fi
  
  # 4. 智能模糊猜想参数 (核心增强)
  local fuzz_params=$(smart_fuzz_params "$api")
  while IFS= read -r param; do
    params_list+=("$param")
  done <<< "$fuzz_params"
  
  # 去重输出
  printf '%s\n' "${params_list[@]}" | sort -u
}
```

#### 3.4 参数变异测试

```bash
#==============================================================================
# 参数变异测试 - 对已知参数进行变异，发现更多漏洞
#==============================================================================

# 参数变异函数
mutate_params() {
  local base_params=$1
  local mutated_list=()
  
  # 1. 原始参数
  mutated_list+=("$base_params")
  
  # 2. 空值变异
  mutated_list+=('{"id":null}')
  mutated_list+=('{"id":""}')
  mutated_list+=('{"id":0}')
  
  # 3. 类型变异
  mutated_list+=('{"id":"1"}')    # 数字转字符串
  mutated_list+=('{"id":1}')      # 字符串转数字
  mutated_list+=('{"id":true}')   # 布尔值
  mutated_list+=('{"id":[]}')     # 数组
  mutated_list+=('{"id":{}}')     # 对象
  
  # 4. 边界值变异
  mutated_list+=('{"id":-1}')     # 负数
  mutated_list+=('{"id":0}')      # 零
  mutated_list+=('{"id":999999999}') # 大数
  
  # 5. 特殊字符变异
  mutated_list+=('{"name":"test\"test"}')
  mutated_list+=('{"name":"test'\''test"}')
  mutated_list+=('{"name":"<script>test</script>"}')
  mutated_list+=('{"name":"test OR 1=1"}')
  
  # 6. 数组参数变异
  mutated_list+=('{"ids":[1]}')
  mutated_list+=('{"ids":[1,2,3]}')
  mutated_list+=('{"ids":[]}')
  
  printf '%s\n' "${mutated_list[@]}" | sort -u
}
```

---

### Phase 4: 批量模糊测试引擎

#### 4.1 响应分析函数

```bash
# 判断是否为成功响应 (未授权访问成功)
is_success_response() {
  local response=$1
  local http_code=$2
  
  # HTTP状态码判断
  if [[ "$http_code" =~ ^(200|201|202|204)$ ]]; then
    
    # ========== 排除错误响应 ==========
    
    # 排除: 请求方法不支持
    if echo "$response" | grep -qiE "Request method.*not supported|方法不支持|method not allowed"; then
      return 1
    fi
    
    # 排除: 参数错误
    if echo "$response" | grep -qiE "参数无效|参数错误|invalid.*param|missing.*param|required.*param"; then
      return 1
    fi
    
    # 排除: 需要认证
    if echo "$response" | grep -qiE "未登录|请登录|need.*login|authentication|unauthorized|forbidden"; then
      return 1
    fi
    
    # 排除: 系统错误 (非业务数据)
    if echo "$response" | grep -qE '"result"\s*:\s*16[0-9]{4}'; then
      # result 16xxxx 通常是业务错误码，不是成功响应
      return 1
    fi
    
    # 排除: 错误码
    if echo "$response" | grep -qE '"code"\s*:\s*(400|401|403|404|420|500|502|503)'; then
      return 1
    fi
    
    # 排除: 错误消息
    if echo "$response" | grep -qiE '"error"|"errorMsg"|"errMsg"|"error_msg"'; then
      return 1
    fi
    
    # ========== 检查成功响应 ==========
    
    # 检查成功标识
    if echo "$response" | grep -qE '"result"\s*:\s*1[,\}]'; then
      # result: 1 通常表示成功
      return 0
    fi
    
    if echo "$response" | grep -qE '"success"\s*:\s*true|"code"\s*:\s*0|"code"\s*:\s*200'; then
      return 0
    fi
    
    # 检查是否返回数据结构
    if echo "$response" | grep -qE '"total"|"count"|"size"'; then
      return 0
    fi
    
    if echo "$response" | grep -qE '"results"|"data"\s*:\s*\{|"data"\s*:\s*\[|"list"|"items"|"records"|"rows"'; then
      return 0
    fi
    
    # 检查是否返回数组
    if echo "$response" | grep -qE '^\s*\['; then
      return 0
    fi
  fi
  
  return 1
}

# 判断是否需要切换请求方法
is_method_not_supported() {
  local response=$1
  
  # 检测方法不支持的提示
  if echo "$response" | grep -qiE "Request method.*not supported|方法不支持|method not allowed|GET.*not support|POST.*not support"; then
    return 0
  fi
  
  return 1
}

# 判断是否需要参数模糊测试
needs_param_fuzz() {
  local response=$1
  
  # 检测参数错误提示
  if echo "$response" | grep -qiE "参数无效|参数错误|invalid.*param|missing.*param|required.*param|请求参数"; then
    return 0
  fi
  
  # 检测业务错误码 (可能是参数问题)
  if echo "$response" | grep -qE '"result"\s*:\s*1600(02|03|04|05)'; then
    return 0
  fi
  
  return 1
}

# 判断是否需要认证
is_auth_required() {
  local response=$1
  local http_code=$2
  
  # HTTP 420 (Tomcat认证错误)
  if [[ "$http_code" == "420" ]]; then
    return 0
  fi
  
  # HTTP 401/403
  if [[ "$http_code" =~ ^(401|403)$ ]]; then
    return 0
  fi
  
  # 响应体包含认证错误信息
  if echo "$response" | grep -qiE 'authentication|authorize|login|signin|unauthorized|forbidden|token|session|未登录|请登录|需要登录'; then
    return 0
  fi
  
  if echo "$response" | grep -qE '"code"\s*:\s*(401|403|420)|"result"\s*:\s*109'; then
    return 0
  fi
  
  return 1
}

# 判断是否为404
is_not_found() {
  local response=$1
  local http_code=$2
  
  if [[ "$http_code" == "404" ]]; then
    return 0
  fi
  
  if echo "$response" | grep -qiE 'not found|404|no such|does not exist'; then
    return 0
  fi
  
  return 1
}

# 从响应中提取提示的方法
extract_suggested_method() {
  local response=$1
  
  # 提取响应中提示的方法
  if echo "$response" | grep -qiE "GET.*not support|Request method 'GET' not supported"; then
    echo "POST"
    return 0
  fi
  
  if echo "$response" | grep -qiE "POST.*not support|Request method 'POST' not supported"; then
    echo "GET"
    return 0
  fi
  
  # 默认返回空
  echo ""
}

# 从响应中提取需要的参数名
extract_required_params() {
  local response=$1
  
  # 提取参数名提示
  local params=$(echo "$response" | grep -oE '"[a-zA-Z_][a-zA-Z0-9_]*"' | tr -d '"' | grep -vE 'result|message|msg|code|error|timestamp|status|host|port')
  
  if [[ -n "$params" ]]; then
    echo "$params"
  fi
}
```

#### 4.2 敏感信息检测

```bash
# 敏感字段定义
SENSITIVE_FIELDS=(
  # 密码相关
  "password" "passwd" "pwd" "pass" "passWord" "PassWord"
  
  # Token/密钥相关
  "token" "accessToken" "refreshToken" "access_token" "refresh_token"
  "api_key" "apikey" "apiKey" "secret" "secretKey" "secret_key"
  "privateKey" "private_key" "privateKey" "key" "authKey"
  
  # 个人信息
  "phone" "mobile" "telephone" "cellphone" "tel"
  "email" "mail" "e_mail" "emailAddress"
  "idCard" "id_card" "idcard" "identity" "identityCard" "idNumber"
  "realName" "real_name" "truename" "trueName" "fullName"
  "address" "addr" "location" "homeAddress"
  
  # 金融信息
  "bankCard" "bank_card" "bankAccount" "account" "accountNo"
  "salary" "income" "money" "amount" "balance"
  
  # 加密相关
  "cipher" "encrypt" "decrypt" "salt" "iv"
  
  # 配置相关
  "jdbc" "database" "datasource" "connection" "driver"
  "host" "port" "server" "endpoint"
)

# 检测敏感字段
detect_sensitive_fields() {
  local response=$1
  local api=$2
  local found_fields=()
  
  for field in "${SENSITIVE_FIELDS[@]}"; do
    if echo "$response" | grep -qi "\"$field\""; then
      found_fields+=("$field")
    fi
  done
  
  if [ ${#found_fields[@]} -gt 0 ]; then
    echo "${found_fields[*]}"
  fi
}

# 检测内部IP地址
detect_internal_ips() {
  local response=$1
  
  # 内网IP正则 (192.168.x.x, 10.x.x.x, 172.16-31.x.x)
  echo "$response" | grep -oE '(192\.168\.[0-9]{1,3}\.[0-9]{1,3}|10\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}|172\.(1[6-9]|2[0-9]|3[01])\.[0-9]{1,3}\.[0-9]{1,3})' | sort -u
}

# 检测内部服务地址
detect_internal_services() {
  local response=$1
  
  # http://IP:port 格式
  echo "$response" | grep -oE 'https?://[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}:[0-9]+' | sort -u
}

# 检测敏感配置
detect_sensitive_config() {
  local response=$1
  local found_config=()
  
  local config_patterns=(
    "cipher_pass" "cipher_token" "auth_code"
    "jdbc_url" "database_url" "db_password"
    "smtp_host" "smtp_password" "mail_password"
    "aws_key" "oss_key" "accessKey"
  )
  
  for pattern in "${config_patterns[@]}"; do
    if echo "$response" | grep -qi "$pattern"; then
      found_config+=("$pattern")
    fi
  done
  
  if [ ${#found_config[@]} -gt 0 ]; then
    echo "${found_config[*]}"
  fi
}
```

#### 4.3 单个API完整测试 (智能响应处理版)

```bash
test_single_api() {
  local api=$1
  local base_url=$2
  local result_dir=$3
  
  # 处理路径参数 (替换 :id, {id} 等)
  local test_api=$(echo "$api" | sed -E 's/:[a-zA-Z_]+/1/g' | sed -E 's/\{[a-zA-Z_]+\}/1/g')
  
  # 生成测试参数集 (包含智能猜想)
  local params_list=$(generate_test_params "$api")
  local params_count=$(echo "$params_list" | wc -l)
  
  log_fuzz "测试API: $api (参数组数: $params_count)"
  
  local test_results=()
  local found_vuln=false
  local need_post_fuzz=false
  local suggested_method=""
  
  # ==================== GET请求测试 ====================
  local get_url="${base_url}${test_api}"
  local get_response=$(curl -sk -X GET "$get_url" \
    -H "Accept: application/json, text/plain, */*" \
    -H "User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36" \
    --connect-timeout $CONNECT_TIMEOUT --max-time $MAX_TIMEOUT \
    -w "\n%{http_code}" 2>/dev/null)
  
  local get_http_code=$(echo "$get_response" | tail -1)
  local get_body=$(echo "$get_response" | sed '$d')
  
  # ========== 智能响应分析 ==========
  
  # 1. 检查是否成功返回数据
  if is_success_response "$get_body" "$get_http_code"; then
    found_vuln=true
    local vuln_type="未授权访问-GET"
    local sensitive=$(detect_sensitive_fields "$get_body" "$api")
    local internal_ips=$(detect_internal_ips "$get_body")
    local internal_services=$(detect_internal_services "$get_body")
    
    log_vuln "[$vuln_type] $api (HTTP $get_http_code)"
    save_evidence "$api" "GET" "{}" "$get_body" "$get_http_code" "$vuln_type" "$sensitive" "$internal_ips" "$internal_services"
    test_results+=("GET|${get_http_code}|SUCCESS|{}")
  
  # 2. 检查是否提示方法不支持 -> 需要切换到POST
  elif is_method_not_supported "$get_body"; then
    suggested_method=$(extract_suggested_method "$get_body")
    log_fuzz "GET不支持，响应提示切换方法: $suggested_method"
    need_post_fuzz=true
  
  # 3. 检查是否需要参数模糊测试
  elif needs_param_fuzz "$get_body"; then
    log_fuzz "GET响应提示需要参数: $(extract_required_params "$get_body")"
    need_post_fuzz=true
  
  # 4. 检查是否需要认证
  elif is_auth_required "$get_body" "$get_http_code"; then
    test_results+=("GET|${get_http_code}|AUTH|{}")
  
  # 5. 检查是否404
  elif is_not_found "$get_body" "$get_http_code"; then
    test_results+=("GET|${get_http_code}|404|{}")
  
  # 6. 其他情况，尝试POST
  else
    need_post_fuzz=true
  fi
  
  # ==================== POST请求测试 (智能参数模糊测试) ====================
  if [[ "$need_post_fuzz" == true ]] || [[ "$found_vuln" == false ]]; then
    local tested_count=0
    
    while IFS= read -r params; do
      tested_count=$((tested_count + 1))
      
      # 进度显示 (每10个显示一次)
      if [[ $((tested_count % 10)) -eq 0 ]]; then
        printf "\r    [POST] 参数组: %d/%d" "$tested_count" "$params_count"
      fi
      
      local post_url="${base_url}${test_api}"
      local post_response=$(curl -sk -X POST "$post_url" \
        -H "Content-Type: application/json" \
        -H "Accept: application/json, text/plain, */*" \
        -H "User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36" \
        -d "$params" \
        --connect-timeout $CONNECT_TIMEOUT --max-time $MAX_TIMEOUT \
        -w "\n%{http_code}" 2>/dev/null)
      
      local post_http_code=$(echo "$post_response" | tail -1)
      local post_body=$(echo "$post_response" | sed '$d')
      
      # 分析POST响应
      if is_success_response "$post_body" "$post_http_code"; then
        found_vuln=true
        local vuln_type="未授权访问-POST"
        local sensitive=$(detect_sensitive_fields "$post_body" "$api")
        local internal_ips=$(detect_internal_ips "$post_body")
        local internal_services=$(detect_internal_services "$post_body")
        
        echo ""
        log_vuln "[$vuln_type] $api 参数: $params (HTTP $post_http_code)"
        save_evidence "$api" "POST" "$params" "$post_body" "$post_http_code" "$vuln_type" "$sensitive" "$internal_ips" "$internal_services"
        test_results+=("POST|${post_http_code}|SUCCESS|$params")
        
        # 发现漏洞后继续测试其他参数，寻找更多数据泄露
      
      # 检查是否需要更多参数
      elif needs_param_fuzz "$post_body"; then
        # 从响应中提取提示的参数名，添加到测试列表
        local hint_params=$(extract_required_params "$post_body")
        if [[ -n "$hint_params" ]]; then
          log_fuzz "响应提示需要参数: $hint_params"
        fi
      
      elif is_auth_required "$post_body" "$post_http_code"; then
        test_results+=("POST|${post_http_code}|AUTH|$params")
        # 如果需要认证，跳过后续测试
        break
      
      elif is_not_found "$post_body" "$post_http_code"; then
        test_results+=("POST|${post_http_code}|404|$params")
        break
      fi
      
    done <<< "$params_list"
  fi
  
  echo ""
  
  # 返回测试结果
  if [[ "$found_vuln" == true ]]; then
    return 0
  else
    return 1
  fi
}

# 保存漏洞证据
save_evidence() {
  local api=$1
  local method=$2
  local params=$3
  local response=$4
  local http_code=$5
  local vuln_type=$6
  local sensitive=$7
  local internal_ips=$8
  local internal_services=$9
  
  local safe_name=$(echo "$api" | sed 's/[\/]/_/g')
  local evidence_file="${OUTPUT_DIR}/evidence/${safe_name}_${method}.json"
  
  # 构建JSON证据
  cat > "$evidence_file" << EOF
{
  "api": "$api",
  "method": "$method",
  "params": $params,
  "http_code": $http_code,
  "vuln_type": "$vuln_type",
  "sensitive_fields": "$sensitive",
  "internal_ips": "$internal_ips",
  "internal_services": "$internal_services",
  "response_length": ${#response},
  "timestamp": "$(date -Iseconds)",
  "response": $(echo "$response" | python3 -c "import sys,json; print(json.dumps(sys.stdin.read()))" 2>/dev/null || echo '""')
}
EOF
  
  # 同时保存可读格式
  local txt_file="${OUTPUT_DIR}/evidence/${safe_name}_${method}.txt"
  {
    echo "=== 漏洞证据 ==="
    echo "API: $api"
    echo "方法: $method"
    echo "参数: $params"
    echo "HTTP状态码: $http_code"
    echo "漏洞类型: $vuln_type"
    echo "敏感字段: $sensitive"
    echo "内网IP: $internal_ips"
    echo "内部服务: $internal_services"
    echo ""
    echo "=== 响应内容 ==="
    echo "$response" | python3 -m json.tool 2>/dev/null || echo "$response"
  } > "$txt_file"
}
```

#### 4.4 批量测试执行

```bash
run_batch_test() {
  log_info "Phase 4: 开始批量模糊测试..."
  
  local api_list="${OUTPUT_DIR}/apis/all_apis.txt"
  local total=$(wc -l < "$api_list")
  local current=0
  local found_count=0
  local auth_count=0
  local notfound_count=0
  local error_count=0
  
  echo ""
  echo "=========================================="
  echo "  开始测试 $total 个API"
  echo "=========================================="
  
  # 创建结果统计文件
  echo "" > "${OUTPUT_DIR}/reports/unauth_apis.txt"
  echo "" > "${OUTPUT_DIR}/reports/auth_required.txt"
  echo "" > "${OUTPUT_DIR}/reports/not_found.txt"
  
  while IFS= read -r api; do
    current=$((current + 1))
    
    # 进度显示
    printf "\r[%4d/%4d] 发现: %d | 需认证: %d | 404: %d | 错误: %d" \
      "$current" "$total" "$found_count" "$auth_count" "$notfound_count" "$error_count"
    
    # 执行测试
    if test_single_api "$api" "$TARGET_URL" "${OUTPUT_DIR}/evidence"; then
      found_count=$((found_count + 1))
      echo "$api" >> "${OUTPUT_DIR}/reports/unauth_apis.txt"
    fi
    
  done < "$api_list"
  
  echo ""
  echo ""
  echo "=========================================="
  echo "  测试完成"
  echo "=========================================="
  log_found "总测试API: $total"
  log_vuln "未授权访问: $found_count"
  log_info "需要认证: $auth_count"
  log_info "404不存在: $notfound_count"
  log_warn "测试错误: $error_count"
}
```

---

### Phase 5: 报告生成

#### 5.1 生成详细测试报告

```bash
generate_report() {
  log_info "Phase 5: 生成测试报告..."
  
  local report_file="${OUTPUT_DIR}/reports/report.md"
  local unauth_file="${OUTPUT_DIR}/reports/unauth_apis.txt"
  local evidence_dir="${OUTPUT_DIR}/evidence"
  
  # 统计数据
  local total_apis=$(wc -l < "${OUTPUT_DIR}/apis/all_apis.txt")
  local unauth_count=$(wc -l < "$unauth_file" 2>/dev/null || echo 0)
  
  # 生成Markdown报告
  cat > "$report_file" << 'HEADER'
# API未授权访问安全测试报告

## 测试概述

| 项目 | 内容 |
|------|------|
| 目标URL | TARGET_URL_PLACEHOLDER |
| 测试时间 | TEST_TIME_PLACEHOLDER |
| 测试API数量 | TOTAL_APIS_PLACEHOLDER |
| 发现漏洞数 | VULN_COUNT_PLACEHOLDER |

---

## 漏洞统计

| 风险等级 | 漏洞类型 | 数量 |
|----------|----------|------|
| 🔴 高危 | 未授权访问 | UNAUTH_COUNT_PLACEHOLDER |
| 🔴 高危 | 敏感信息泄露 | SENSITIVE_COUNT_PLACEHOLDER |
| 🟡 中危 | 内部信息泄露 | INTERNAL_COUNT_PLACEHOLDER |
| 🟡 中危 | DoS风险 | DOS_COUNT_PLACEHOLDER |

---

## 漏洞详情

HEADER

  # 替换占位符
  sed -i "s|TARGET_URL_PLACEHOLDER|$TARGET_URL|g" "$report_file"
  sed -i "s|TEST_TIME_PLACEHOLDER|$(date)|g" "$report_file"
  sed -i "s|TOTAL_APIS_PLACEHOLDER|$total_apis|g" "$report_file"
  sed -i "s|UNAUTH_COUNT_PLACEHOLDER|$unauth_count|g" "$report_file"
  
  # 添加漏洞详情
  local vuln_num=1
  for evidence in "$evidence_dir"/*.json; do
    if [[ -f "$evidence" ]]; then
      local api=$(python3 -c "import json; print(json.load(open('$evidence'))['api'])")
      local method=$(python3 -c "import json; print(json.load(open('$evidence'))['method'])")
      local params=$(python3 -c "import json; print(json.load(open('$evidence'))['params'])")
      local http_code=$(python3 -c "import json; print(json.load(open('$evidence'))['http_code'])")
      local sensitive=$(python3 -c "import json; print(json.load(open('$evidence'))['sensitive_fields'])")
      local internal_ips=$(python3 -c "import json; print(json.load(open('$evidence'))['internal_ips'])")
      
      cat >> "$report_file" << EOF

### 漏洞$vuln_num: \`$api\`

| 属性 | 值 |
|------|-----|
| 接口 | \`$api\` |
| 方法 | $method |
| 参数 | \`$params\` |
| HTTP状态码 | $http_code |
| 敏感字段 | $sensitive |
| 内网IP | $internal_ips |

**cURL复现命令:**
\`\`\`bash
curl -sk -X $method "${TARGET_URL}${api}" \\
  -H "Content-Type: application/json" \\
  -d '$params'
\`\`\`

---

EOF
      vuln_num=$((vuln_num + 1))
    fi
  done
  
  # 添加修复建议
  cat >> "$report_file" << 'FOOTER'

## 修复建议

### 1. 添加认证机制 (紧急)

```java
// Spring Security配置示例
@Configuration
@EnableWebSecurity
public class SecurityConfig extends WebSecurityConfigurerAdapter {
    @Override
    protected void configure(HttpSecurity http) throws Exception {
        http.authorizeRequests()
            .antMatchers("/api/**").authenticated()
            .and()
            .sessionManagement().sessionCreationPolicy(SessionCreationPolicy.STATELESS);
    }
}
```

### 2. 接口权限控制

```java
@RestController
@RequestMapping("/api")
public class ApiController {
    
    @PreAuthorize("hasRole('USER')")
    @PostMapping("/sensitive")
    public Result sensitiveApi(@RequestBody Params params) {
        // 业务逻辑
    }
}
```

### 3. 敏感信息脱敏

```java
// 返回前脱敏处理
public class SensitiveDataSerializer extends JsonSerializer<String> {
    @Override
    public void serialize(String value, JsonGenerator gen, SerializerProvider provider) {
        gen.writeString(maskSensitive(value));
    }
}
```

### 4. 分页限制

```java
@PostMapping("/list")
public Result listData(@RequestBody QueryParams params) {
    // 限制单次最大返回100条
    if (params.getPageSize() == null || params.getPageSize() > 100) {
        params.setPageSize(100);
    }
    return service.listData(params);
}
```

### 5. 日志审计

```java
@Aspect
public class ApiAuditAspect {
    @Around("@annotation(ApiEndpoint)")
    public Object audit(ProceedingJoinPoint pjp) {
        // 记录API访问日志
        log.info("API访问: {} - 用户: {} - 参数: {}", 
            pjp.getSignature(), getCurrentUser(), pjp.getArgs());
        return pjp.proceed();
    }
}
```

---

## 测试工具信息

- 工具名称: API未授权模糊测试工具 v3.0
- 测试方法: GET + POST (多参数组合)
- 参数构造: JS上下文分析 + 智能推断 + 模糊猜想

---

*报告生成时间: $(date)*
FOOTER

  log_found "报告已生成: $report_file"
}
```

---

### Phase 6: 主程序入口

```bash
main() {
  echo ""
  echo "=========================================="
  echo "  API未授权模糊测试工具 v3.0"
  echo "=========================================="
  log_info "目标: $TARGET_URL"
  log_info "输出: $OUTPUT_DIR"
  echo ""
  
  # Phase 1: 信息收集
  scan_entry_pages
  extract_js_files
  
  # Phase 2: API提取与分析
  extract_api_paths
  analyze_api_context
  
  # ===== Phase 2.5: 新增通用路径模糊测试 =====
  log_info "Phase 2.5: 站点特征匹配与路径模糊测试"
  local tech_stack=$(detect_tech_stack)
  local dict_file=$(load_path_dict_by_tech "$tech_stack")
  run_path_fuzz "$dict_file" 80
  quick_verify_findings
  
  # Phase 3 & 4: 模糊测试
  run_batch_test
  
  # Phase 5: 报告生成
  generate_report
  
  echo ""
  log_found "测试完成! 结果目录: $OUTPUT_DIR"
  log_found "模糊测试200结果: ${OUTPUT_DIR}/fuzz_dicts/fuzz_200.txt"
  log_found "查看报告: cat ${OUTPUT_DIR}/reports/report.md"
}

# 执行主程序
main "$@"
```

---

## 使用方法

### 基本使用

```bash
# 赋予执行权限
chmod +x api_unauth_fuzz.sh

# 执行测试
./api_unauth_fuzz.sh https://target.com

# 查看结果
cat api_fuzz_*/reports/report.md
```

### 输出目录结构

```
api_fuzz_YYYYMMDD_HHMMSS/
├── js/                    # 下载的JS文件
│   ├── app.js
│   └── all_merged.js      # 合并后的JS
├── apis/                  # API提取结果
│   ├── all_apis.txt       # 所有API列表
│   ├── all_raw_apis.txt   # 原始提取结果
│   └── analysis/          # API上下文分析
│       └── _api_.txt
├── evidence/              # 漏洞证据
│   ├── _api__GET.json
│   └── _api__POST.json
├── reports/               # 测试报告
│   ├── report.md          # 主报告
│   ├── unauth_apis.txt    # 未授权API列表
│   ├── auth_required.txt  # 需认证API列表
│   └── not_found.txt      # 404 API列表
├── fuzz_dicts/            # ★ 新增: 站点特征模糊测试
│   ├── tech_stack.txt     #   识别到的技术栈
│   ├── targeted_paths.txt #   基于技术栈的路径字典
│   ├── fuzz_results.txt   #   所有请求完整结果
│   ├── fuzz_200.txt       #   HTTP 200 结果 (路径|方法|状态|字节长度|耗时)
│   ├── fuzz_403.txt       #   HTTP 403 结果
│   ├── fuzz_5xx.txt       #   HTTP 5xx 结果
│   └── verify_results.txt #   二次验证结果
└── entry_*.html           # 入口页面
```

---

## 核心特性

| 特性 | 说明 |
|------|------|
| JS深度提取 | 9种模式提取API路径 |
| 上下文分析 | 分析API调用上下文提取真实参数 |
| 智能推断 | 根据API命名推断类型和参数 |
| **智能模糊猜想** | **无法提取参数时自动猜想构造** |
| 多参数测试 | 每个API测试20+组参数组合 |
| 参数变异 | 对已知参数进行变异测试 |
| 双方法测试 | 同时测试GET和POST |
| 敏感检测 | 自动检测敏感字段、内网IP、配置泄露 |
| 证据保存 | 完整保存请求响应证据 |
| 报告生成 | Markdown格式详细报告 |
| ★ **技术栈探测** | **从JS/HTML/Header自动识别Vue/React/Angular/Spring等框架** |
| ★ **路径字典匹配** | **基于技术栈加载已知敏感路径，匹配网上公开的未授权路径** |
| ★ **速率控制fuzz** | **~80 req/s带20%随机抖动脉冲，最大10万条，不太快也不太慢** |
| ★ **200结果输出** | **自动输出HTTP 200路径+字节长度+方法，标记JSON/敏感数据/大响应** |
| ★ **二次验证** | **对发现的路径进行GET/POST双重验证确认** |

---

## 智能模糊猜想策略

当无法从JS上下文提取到真实参数时，系统会自动启用智能模糊猜想模式：

### 策略1: 基础参数组合
- 空参数 `{}`
- 通用分页参数
- ID参数变体

### 策略2: API路径关键词匹配
- 检测 `id/Id/ID` → 生成ID参数
- 检测 `list/query/search` → 生成分页参数
- 检测 `detail/info/view` → 生成详情参数
- 检测 `add/create/save` → 生成创建参数
- 检测 `update/edit` → 生成更新参数
- 检测 `delete/remove` → 生成删除参数

### 策略3: 业务领域关键词匹配
- 用户相关: `userId, userName, phone, email`
- 组织相关: `unitId, orgId, corporateCode`
- 任务相关: `taskId, taskStatus, taskType`
- 产品相关: `productId, productName, productType`
- 日志相关: `startTime, endTime, module`
- 字典相关: `dictType, code, key`

### 策略4: 路径参数提取
- 从 `/user/:id` 提取 `id` 参数
- 从 `/org/{orgId}` 提取 `orgId` 参数

### 策略5: 通用模糊测试参数
- 常用字段组合
- 时间范围参数
- 大数据量测试 (DoS检测)
- 排序参数

### 策略6: 路径单词提取
- 从 `/userProfile/getDetail` 提取 `user, profile, get, detail`
- 生成 `userId, userProfileId, profileId` 等参数

---

## 测试参数策略

1. **空参数测试**: `{}`
2. **JS上下文提取**: 从代码中提取真实参数
3. **类型推断参数**: 根据API路径推断
4. **智能模糊猜想**: 无法提取时自动猜想
5. **参数变异测试**: 对已知参数变异
6. **分页参数**: `{page, pageSize}` 多种变体
7. **ID参数**: `{id}` 多种命名变体
8. **大数据测试**: `pageSize=10000` DoS检测
9. **关键词匹配**: 根据user/org/task等关键词构造特定参数

---

## 智能响应判断策略

### 响应判断流程

```
响应内容分析
    │
    ├─► "Request method 'GET' not supported" 
    │       └─► 自动切换POST方法测试
    │
    ├─► "Request method 'POST' not supported"
    │       └─► 自动切换GET方法测试
    │
    ├─► "参数无效/参数错误/invalid param"
    │       └─► 启用智能参数模糊测试
    │
    ├─► "result": 16xxxx (业务错误码)
    │       └─► 排除，非成功响应
    │
    ├─► "result": 1 或 "success": true
    │       └─► 确认为成功响应
    │
    ├─► "未登录/请登录/need login"
    │       └─► 标记为需要认证
    │
    └─► 返回数据结构 (data/list/total等)
            └─► 确认为未授权访问成功
```

### 常见响应码处理

| 响应内容 | 处理策略 |
|----------|----------|
| `"result": 160011` + "method not supported" | 切换请求方法 |
| `"result": 160002` + "参数无效" | 智能参数模糊测试 |
| `"result": 160001` + "系统错误" | 可能需要特定参数 |
| `"result": 109` | 需要登录认证 |
| `"result": 1` + `"data"` | 成功响应，存在漏洞 |

---

*技能版本: v3.1*
*更新时间: 2026-05-09*
