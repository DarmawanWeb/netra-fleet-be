#!/usr/bin/env python3
"""Check a netra-api running without a license: core only, one robot (pip install requests)."""
import os
import sys
import uuid

import requests

API = os.environ.get('NETRA_URL', 'http://localhost:8080')
USER, PASSWORD = os.environ.get('NETRA_USER', 'admin'), os.environ.get('NETRA_PASSWORD', 'admin')


def check(cond, what):
    """Fail loudly."""
    print(('ok   ' if cond else 'FAIL ') + what)
    if not cond:
        sys.exit(1)


s = requests.Session()
s.headers['Authorization'] = 'Bearer ' + s.post(f'{API}/api/v1/auth/login',
                                                json={'username': USER, 'password': PASSWORD}).json()['token']
info = s.get(f'{API}/api/v1/info').json()
check(info['license']['status'] in ('missing', 'invalid') and info['features'] == [], 'no license: no modules')
check(requests.get(f'{API}/api/v1/branding').json()['name'] == 'NETRA', 'default branding')
for path in ('anomalies', 'crowd/heatmap', 'reports/summary', 'audit', 'api-clients'):
    r = s.get(f'{API}/api/v1/{path}')
    check(r.status_code == 403 and r.json().get('error') == 'feature not licensed', f'{path} needs a license')
check(requests.get(f'{API}/api/ext/v1/openapi.yaml').status_code == 403, 'integration API needs a license')
check(s.get(f'{API}/api/v1/patrols').status_code == 200, 'core works without a license')

existing = len(s.get(f'{API}/api/v1/robots').json())
made = []
for _ in range(max(0, 1 - existing)):
    rid = f'eval-{uuid.uuid4().hex[:6]}'
    check(s.post(f'{API}/api/v1/robots', json={'id': rid}).status_code == 201, 'first robot registered')
    made.append(rid)
r = s.post(f'{API}/api/v1/robots', json={'id': f'eval-{uuid.uuid4().hex[:6]}'})
check(r.status_code == 403 and r.json().get('robot_limit') == 1, 'robot limit without a license')
for rid in made:
    s.delete(f'{API}/api/v1/robots/{rid}')
print('unlicensed smoke test passed')
