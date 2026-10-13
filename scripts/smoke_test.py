#!/usr/bin/env python3
"""End-to-end check of a running netra-api with a simulated robot (pip install requests websocket-client)."""
import base64
import json
import os
import sys
import threading
import time
import uuid

import requests
import websocket

API = os.environ.get('NETRA_URL', 'http://localhost:8080')
USER, PASSWORD = os.environ.get('NETRA_USER', 'admin'), os.environ.get('NETRA_PASSWORD', 'admin')


def check(cond, what):
    """Fail loudly."""
    print(('ok   ' if cond else 'FAIL ') + what)
    if not cond:
        sys.exit(1)


s = requests.Session()
token = s.post(f'{API}/api/v1/auth/login', json={'username': USER, 'password': PASSWORD}).json()['token']
s.headers['Authorization'] = f'Bearer {token}'
robot_id = f'smoke-{uuid.uuid4().hex[:6]}'
robot_token = s.post(f'{API}/api/v1/robots', json={'id': robot_id, 'name': 'Smoke L1W'}).json()['token']
rh = {'Authorization': f'Bearer {robot_token}', 'X-Robot-Id': robot_id}

inbox = []
ws = websocket.create_connection(API.replace('http', 'ws') + '/api/v1/robot/ws',
                                 header=[f'{k}: {v}' for k, v in rh.items()])
threading.Thread(target=lambda: [inbox.append(json.loads(ws.recv())) for _ in iter(int, 1)],
                 daemon=True).start()
ws.send(json.dumps({'type': 'hello', 'robot_id': robot_id, 'version': 1}))
ws.send(json.dumps({'type': 'telemetry', 'pose': {'x': 1, 'y': 2, 'yaw': 0}, 'nav': {'state': 'NAV_INACTIVE'}}))

csv = '# x,y,yaw_deg,dwell_sec\n0,0,0,0\n5,0,90,10\n5,5,180,0\n0,0,0,0\n'
meta = {'uid': str(uuid.uuid4()), 'name': 'Koridor Lt1', 'recorded_at': time.time(), 'length_m': 17.1,
        'stops': 1, 'closed': True}
for _ in range(2):  # second upload must be idempotent
    r = requests.post(f'{API}/api/v1/robot/routes', headers=rh, data={'meta': json.dumps(meta)},
                      files={'route.csv': ('route.csv', csv, 'text/csv')})
route_id = r.json()['id']
routes = s.get(f'{API}/api/v1/routes', params={'robot_id': robot_id}).json()
check(len(routes) == 1, 'route uploaded once')
check(len(s.get(f'{API}/api/v1/routes/{route_id}').json()['points']) == 4, 'route points parsed')
robots = {x['id']: x for x in s.get(f'{API}/api/v1/robots').json()}
check(robots[robot_id]['online'] and robots[robot_id]['last_telemetry'], 'robot online with telemetry')

patrol_id = s.post(f'{API}/api/v1/patrols', json={'robot_id': robot_id, 'route_id': route_id}).json()['id']
time.sleep(0.5)
cmd = next(m for m in inbox if m.get('type') == 'command')
check(cmd['name'] == 'start_patrol' and cmd['args']['patrol_id'] == patrol_id, 'robot got start_patrol')
file = requests.get(f'{API}/api/v1/robot/routes/{route_id}/file', headers=rh).text
check(file == csv, 'robot downloaded the route')
ws.send(json.dumps({'type': 'ack', 'id': cmd['id'], 'ok': True, 'msg': 'patrol started'}))
ctx = {'patrol_id': patrol_id, 'route_id': route_id, 'ts': time.time()}
ws.send(json.dumps({'type': 'patrol_event', 'event': 'started', **ctx}))
ws.send(json.dumps({'type': 'stop_event', 'index': 1, 'event': 'arrived', 'x': 5, 'y': 0, **ctx}))
ws.send(json.dumps({'type': 'crowd', 'ts_start': time.time() - 60, 'ts_end': time.time(), 'cell_size': 1.0,
                    'cells': [[1, 2, 3], [4, 4, 1]], 'peak_people': 4, **ctx}))
time.sleep(0.3)
check(s.get(f'{API}/api/v1/patrols/{patrol_id}').json()['patrol']['status'] == 'running', 'patrol running')

jpeg = base64.b64decode(
    '/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAA0JCgsKCA0LCgsODg0PEyAVExISEyccHhcgLikxMC4pLSwzOko+MzZGNywtQFdBRkxOUlNSMj5aYVpQYEpRUk//2wBDAQ4ODhMREyYVFSZPNS01T09PT09PT09PT09PT09PT09PT09PT09PT09PT09PT09PT09PT09PT09PT09PT09PT0//wAARCAAYACADASIAAhEBAxEB/8QAHwAAAQUBAQEBAQEAAAAAAAAAAAECAwQFBgcICQoL/8QAtRAAAgEDAwIEAwUFBAQAAAF9AQIDAAQRBRIhMUEGE1FhByJxFDKBkaEII0KxwRVS0fAkM2JyggkKFhcYGRolJicoKSo0NTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqDhIWGh4iJipKTlJWWl5iZmqKjpKWmp6ipqrKztLW2t7i5usLDxMXGx8jJytLT1NXW19jZ2uHi4+Tl5ufo6erx8vP09fb3+Pn6/8QAHwEAAwEBAQEBAQEBAQAAAAAAAAECAwQFBgcICQoL/8QAtREAAgECBAQDBAcFBAQAAQJ3AAECAxEEBSExBhJBUQdhcRMiMoEIFEKRobHBCSMzUvAVYnLRChYkNOEl8RcYGRomJygpKjU2Nzg5OkNERUZHSElKU1RVVldYWVpjZGVmZ2hpanN0dXZ3eHl6goOEhYaHiImKkpOUlZaXmJmaoqOkpaanqKmqsrO0tba3uLm6wsPExcbHyMnK0tPU1dbX2Nna4uPk5ebn6Onq8vP09fb3+Pn6/9oADAMBAAIRAxEAPwCKiiigAooooAKKKKACiiigD//Z')
ids = {}
for category in ('trash', 'floor_damage'):
    am = {'uid': str(uuid.uuid4()), 'category': category, 'confidence': 0.9, 'description': 'test',
          'pose': {'x': 5.0, 'y': 0.1, 'yaw': 0.0}, 'stop_index': 1, 'detector': 'robot', **ctx}
    r = requests.post(f'{API}/api/v1/robot/anomalies', headers=rh, data={'meta': json.dumps(am)},
                      files={'image.jpg': ('image.jpg', jpeg, 'image/jpeg')})
    ids[category] = r.json()['id']
time.sleep(1.5)
a = {k: s.get(f'{API}/api/v1/anomalies/{v}').json() for k, v in ids.items()}
check(a['trash']['status'] == 'confirmed', 'trash auto-confirmed (no validation rule)')
check(a['floor_damage']['status'] == 'needs_validation', 'floor damage waits for validation')
r = s.post(f'{API}/api/v1/anomalies/{ids["floor_damage"]}/validate', json={'decision': 'confirm', 'note': 'ok'})
check(r.status_code == 204, 'operator confirmed floor damage')
check(s.get(f'{API}/api/v1/anomalies/{ids["trash"]}/image').content == jpeg, 'anomaly image served')

ws.send(json.dumps({'type': 'patrol_event', 'event': 'finished', 'reason': 'goal_reached', **ctx}))
time.sleep(0.3)
detail = s.get(f'{API}/api/v1/patrols/{patrol_id}').json()
check(detail['patrol']['status'] == 'finished', 'patrol finished')
check(detail['recap']['anomalies'] == {'trash': 1, 'floor_damage': 1} and detail['recap']['crowd_peak'] == 4,
      f'recap {detail["recap"]}')
heat = s.get(f'{API}/api/v1/crowd/heatmap', params={'robot_id': robot_id}).json()
check(sorted((c['x'], c['y'], c['peak']) for c in heat) == [(1, 2, 3), (4, 4, 1)], 'heatmap cells')
check(s.get(f'{API}/api/v1/crowd/timeline', params={'robot_id': robot_id}).json()[0]['peak'] == 4,
      'crowd timeline')
sched = s.post(f'{API}/api/v1/schedules', json={'robot_id': robot_id, 'route_id': route_id, 'days': [1, 3, 5],
                                                 'time_of_day': '09:30'}).json()
check(any(x['id'] == sched['id'] for x in s.get(f'{API}/api/v1/schedules').json()), 'schedule saved')
check(requests.get(f'{API}/api/v1/robots').status_code == 401, 'dashboard API needs login')

# reports
pdf = s.get(f'{API}/api/v1/reports/patrols/{patrol_id}/pdf')
check(pdf.status_code == 200 and pdf.content[:4] == b'%PDF', f'patrol PDF ({len(pdf.content)} bytes)')
check(s.get(f'{API}/api/v1/reports/summary', params={'format': 'pdf'}).content[:4] == b'%PDF', 'summary PDF')
for path in ('summary?format=xlsx', 'anomalies', 'patrols'):
    x = s.get(f'{API}/api/v1/reports/{path}')
    check(x.status_code == 200 and x.content[:2] == b'PK', f'{path} XLSX')
check(isinstance(s.get(f'{API}/api/v1/robots/{robot_id}/track').json(), list), 'track history')

# e-stop from the dashboard reaches the robot
r = s.post(f'{API}/api/v1/robots/{robot_id}/command', json={'name': 'estop'})
time.sleep(0.3)
check(r.status_code == 202 and any(m.get('name') == 'estop' for m in inbox if m.get('type') == 'command'),
      'robot got estop')

# RBAC
viewer = f'viewer{uuid.uuid4().hex[:5]}'
check(s.post(f'{API}/api/v1/users', json={'username': viewer, 'role': 'viewer', 'password': 'viewer-pass'}).status_code == 201,
      'admin created a viewer')
v = requests.Session()
login = v.post(f'{API}/api/v1/auth/login', json={'username': viewer, 'password': 'viewer-pass'}).json()
check(login['role'] == 'viewer' and login['permissions'] == ['view'], 'viewer login with permissions')
v.headers['Authorization'] = f"Bearer {login['token']}"
check(v.get(f'{API}/api/v1/patrols').status_code == 200, 'viewer can read')
check(v.post(f'{API}/api/v1/patrols', json={'robot_id': robot_id, 'route_id': route_id}).status_code == 403,
      'viewer cannot start a patrol')
check(v.get(f'{API}/api/v1/users').status_code == 403, 'viewer cannot list users')
check(v.post(f'{API}/api/v1/auth/logout').status_code == 204 and v.get(f'{API}/api/v1/me').status_code == 401, 'logout')
time.sleep(0.5)
audit = s.get(f'{API}/api/v1/audit').json()
check(any(a['action'].startswith('POST /api/v1/patrols') for a in audit) and any(a['action'] == 'login' for a in audit),
      'audit log records actions and logins')
s.delete(f'{API}/api/v1/robots/{robot_id}')
print('smoke test passed')
