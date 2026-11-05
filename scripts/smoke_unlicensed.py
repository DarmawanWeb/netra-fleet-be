#!/usr/bin/env python3
"""Check a netra-api running without a license: core only, one robot (pip install requests)."""
import json
import os
import sys
import uuid

import requests

API = os.environ.get('NETRA_URL', 'http://localhost:8080')
ROBOT_API = os.environ.get('NETRA_ROBOT_URL', API)  # the gateway of a split installation
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
for path in ('anomalies', 'crowd/heatmap', 'reports/summary', 'audit', 'api-clients', 'substation/assets',
             'reports/substation/findings'):
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

# Robots of a deployment without the substation module: inspections are dropped, plans are empty
rid = (made or [x['id'] for x in s.get(f'{API}/api/v1/robots').json()])[0]
rh = {'Authorization': 'Bearer ' + s.post(f'{API}/api/v1/robots/{rid}/token').json()['token']}
r = requests.post(f'{ROBOT_API}/api/v1/robot/inspections', headers=rh,
                  files={'meta': (None, json.dumps({'uid': uuid.uuid4().hex, 'point_id': 1}))})
check(r.status_code == 202 and r.json().get('dropped') == 'feature not licensed', 'inspection uploads dropped')
route = {'uid': uuid.uuid4().hex, 'name': 'Unlicensed round', 'stops': 1}
route_id = requests.post(f'{ROBOT_API}/api/v1/robot/routes', headers=rh, data={'meta': json.dumps(route)},
                         files={'route.csv': ('route.csv', '0,0,0,0\n5,0,0,10\n', 'text/csv')}).json()['id']
plan = requests.get(f'{ROBOT_API}/api/v1/robot/routes/{route_id}/inspection', headers=rh).json()
check(plan == {'route_id': route_id, 'points': []}, 'empty inspection plan')
for rid in made:
    s.delete(f'{API}/api/v1/robots/{rid}')
print('unlicensed smoke test passed')
