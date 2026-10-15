"""Exercise hosted auth and inventory with the restricted CI database role."""
import json
import sys
import urllib.request

BASE = "http://127.0.0.1:8080"

def request(path, data=None, cookie=None):
    headers = {"Content-Type": "application/json", "X-ListenLedger-Request": "1"}
    if cookie:
        headers["Cookie"] = cookie
    req = urllib.request.Request(BASE + path, data=None if data is None else json.dumps(data).encode(), headers=headers)
    with urllib.request.urlopen(req) as response:
        return json.load(response), response.headers.get("Set-Cookie", "").split(";", 1)[0]

credentials = {"email": "deployment-ci@example.com", "password": "CI-only-long-passphrase-2026"}
if "--login-only" not in sys.argv:
    request("/api/auth/signup", {**credentials, "shop_name": "Deployment CI"})
_, cookie = request("/api/auth/login", credentials)
assert cookie, "Login did not set a session cookie"
request("/api/inventory", cookie=cookie)
request("/api/settings", cookie=cookie)
print("Restricted runtime signup/login and authenticated reads passed")
