#!/usr/bin/env python3
"""Substation inspection end to end on a running netra-api with the substation module (pip install
requests websocket-client): assets, inspection points on a route, a simulated robot's thermal and
partial discharge inspections, NETA priorities, findings, trends, exports and the patrol report."""
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
SUB = f'{API}/api/v1/substation'
JPEG = base64.b64decode(
    '/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAA0JCgsKCA0LCgsODg0PEyAVExISEyccHhcgLikxMC4pLSwzOko+MzZGNywtQFdBRkxOUlNSMj5aYVpQ'
    'YEpRUk//2wBDAQ4ODhMREyYVFSZPNS01T09PT09PT09PT09PT09PT09PT09PT09PT09PT09PT09PT09PT09PT09PT09PT09PT0//wAARCAAYACAD'
    'ASIAAhEBAxEB/8QAHwAAAQUBAQEBAQEAAAAAAAAAAAECAwQFBgcICQoL/8QAtRAAAgEDAwIEAwUFBAQAAAF9AQIDAAQRBRIhMUEGE1FhByJxFDKB'
    'kaEII0KxwRVS0fAkM2JyggkKFhcYGRolJicoKSo0NTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqDhIWGh4iJipKTlJWWl5iZ'
    'mqKjpKWmp6ipqrKztLW2t7i5usLDxMXGx8jJytLT1NXW19jZ2uHi4+Tl5ufo6erx8vP09fb3+Pn6/8QAHwEAAwEBAQEBAQEBAQAAAAAAAAECAwQF'
    'BgcICQoL/8QAtREAAgECBAQDBAcFBAQAAQJ3AAECAxEEBSExBhJBUQdhcRMiMoEIFEKRobHBCSMzUvAVYnLRChYkNOEl8RcYGRomJygpKjU2Nzg5'
    'OkNERUZHSElKU1RVVldYWVpjZGVmZ2hpanN0dXZ3eHl6goOEhYaHiImKkpOUlZaXmJmaoqOkpaanqKmqsrO0tba3uLm6wsPExcbHyMnK0tPU1dbX'
    '2Nna4uPk5ebn6Onq8vP09fb3+Pn6/9oADAMBAAIRAxEAPwCKiiigAooooAKKKKACiiigD//Z')


def check(cond, what):
    """Fail loudly."""
    print(('ok   ' if cond else 'FAIL ') + what)
    if not cond:
        sys.exit(1)


def collect(sock, into):
    """Keep the JSON messages of a WebSocket until it closes."""
    try:
        while True:
            into.append(json.loads(sock.recv()))
    except (websocket.WebSocketException, ValueError, OSError):
        pass


s = requests.Session()
token = s.post(f'{API}/api/v1/auth/login', json={'username': USER, 'password': PASSWORD}).json()['token']
s.headers['Authorization'] = f'Bearer {token}'
check('substation' in s.get(f'{API}/api/v1/info').json()['features'], 'substation module licensed')

# The live feed of the dashboard
live = []
dash = websocket.create_connection(API.replace('http', 'ws') + f'/api/v1/ws?token={token}')
threading.Thread(target=collect, args=(dash, live), daemon=True).start()

# A robot with a route of two stops
robot_id, other_id = f'gi-{uuid.uuid4().hex[:6]}', f'gi-{uuid.uuid4().hex[:6]}'
rh = {'Authorization': 'Bearer ' + s.post(f'{API}/api/v1/robots', json={'id': robot_id, 'name': 'GI Patrol'}).json()['token'],
      'X-Robot-Id': robot_id}
oh = {'Authorization': 'Bearer ' + s.post(f'{API}/api/v1/robots', json={'id': other_id}).json()['token'],
      'X-Robot-Id': other_id}
inbox = []
ws = websocket.create_connection(API.replace('http', 'ws') + '/api/v1/robot/ws', header=[f'{k}: {v}' for k, v in rh.items()])
threading.Thread(target=collect, args=(ws, inbox), daemon=True).start()
ws.send(json.dumps({'type': 'hello', 'robot_id': robot_id, 'version': 1}))
csv = '# x,y,yaw_deg,dwell_sec\n0,0,0,0\n10,0,90,15\n10,10,180,20\n0,0,0,0\n'
meta = {'uid': str(uuid.uuid4()), 'name': 'Bay 1 round', 'recorded_at': time.time(), 'length_m': 34, 'stops': 2,
        'closed': True}
route_id = requests.post(f'{API}/api/v1/robot/routes', headers=rh, data={'meta': json.dumps(meta)},
                         files={'route.csv': ('route.csv', csv, 'text/csv')}).json()['id']

# Settings and criteria
settings = s.get(f'{SUB}/settings').json()
check(settings['finding_priority'] == 3 and settings['alert_priority'] == 2 and settings['pd']['alarm_db'] == 20,
      'default substation settings')
check(s.put(f'{SUB}/settings', json={'finding_priority': 5}).status_code == 400, 'finding priority 5 refused')
r = s.put(f'{SUB}/settings', json={'pd': {'watch_db': 10, 'alarm_db': 20}, 'telegram_chat_id': ' -100123 '})
check(r.status_code == 200 and r.json()['telegram_chat_id'] == '-100123' and r.json()['finding_priority'] == 3,
      'settings saved, unsent fields kept')
criteria = s.get(f'{SUB}/criteria').json()
check([c['priority'] for c in criteria['thermal']] == [1, 2, 3, 4] and criteria['thermal'][0]['similar'] == '> 15 °C'
      and 'disconnector' in criteria['asset_kinds'], 'NETA table and asset kinds')

# Assets
tag = f'PMS-{uuid.uuid4().hex[:4]}'
pms = s.post(f'{SUB}/assets', json={'tag': tag, 'name': 'Disconnector line 1', 'kind': 'disconnector', 'bay': 'Line 1',
                                    'voltage_kv': 150, 'x': 10, 'y': 3})
check(pms.status_code == 201 and pms.json()['priority'] is None and pms.json()['open_findings'] == 0, 'asset created')
pms = pms.json()
trf = s.post(f'{SUB}/assets', json={'tag': f'TRF-{uuid.uuid4().hex[:4]}', 'name': 'Transformer 1',
                                    'kind': 'transformer', 'bay': 'Trafo 1', 'voltage_kv': 150}).json()
check(s.post(f'{SUB}/assets', json={'tag': tag.lower(), 'name': 'Twin', 'kind': 'breaker'}).status_code == 409,
      'tag taken, ignoring case')
check(s.post(f'{SUB}/assets', json={'tag': 'X1', 'name': 'Bad', 'kind': 'toaster'}).status_code == 400, 'unknown kind refused')
check(s.post(f'{SUB}/assets', json={'tag': 'X2', 'name': 'Half', 'kind': 'other', 'x': 1}).status_code == 400,
      'x without y refused')

# Inspection points: a thermal point at stop 0 (three clamps and a body with a rated limit), pd at stop 1
targets = [{'key': k, 'label': f'Clamp {k.upper()}', 'phase': k.upper(), 'group': 'clamp', 'asset_id': pms['id'],
            'roi': {'x': 0.1 + 0.3 * i, 'y': 0.4, 'w': 0.2, 'h': 0.2}} for i, k in enumerate(('r', 's', 't'))]
targets.append({'key': 'body', 'label': 'Body', 'limit_c': 90, 'roi': {'x': 0.0, 'y': 0.0, 'w': 1.0, 'h': 0.3}})
point = {'name': 'Line 1 disconnector', 'kind': 'thermal', 'asset_id': pms['id'], 'route_id': route_id, 'stop_index': 0,
         'ptz': {'pan_deg': -30, 'tilt_deg': 12, 'zoom': 4}, 'settings': {'emissivity': 0.95}, 'targets': targets}
check(s.post(f'{SUB}/points', json={**point, 'stop_index': 2}).status_code == 400, 'stop beyond the route refused')
check(s.post(f'{SUB}/points', json={**point, 'asset_id': 999999}).status_code == 400, 'missing asset refused')
check(s.post(f'{SUB}/points', json={**point, 'targets': [{**targets[0], 'roi': {'x': 0.9, 'y': 0, 'w': 0.2, 'h': 1}}]})
      .status_code == 400, 'region outside the image refused')
r = s.post(f'{SUB}/points', json=point)
check(r.status_code == 201 and r.json()['stance'] == {'x': 10, 'y': 0, 'yaw_deg': 90} and r.json()['robot_id'] == robot_id,
      'thermal point created at its stop')
thermal = r.json()
pd = s.post(f'{SUB}/points', json={'name': 'Trafo 1 bushing PD', 'kind': 'pd', 'asset_id': trf['id'],
                                   'route_id': route_id, 'stop_index': 1, 'settings': {'watch_db': 12}}).json()
check(s.put(f"{SUB}/points/{pd['id']}", json={'name': 'x', 'kind': 'thermal', 'targets': targets}).status_code == 409,
      'kind cannot change')
points = s.get(f'{SUB}/points', params={'route_id': route_id}).json()
check([p['id'] for p in points] == [thermal['id'], pd['id']], 'points listed by stop')

# The robot's plan, and the robot of another route
plan = requests.get(f'{API}/api/v1/robot/routes/{route_id}/inspection', headers=rh).json()['points']
check(len(plan) == 2 and plan[0]['targets'][3]['limit_c'] == 90 and plan[0]['settings']['reference'] == 'auto'
      and plan[1]['settings'] == {'watch_db': 12, 'alarm_db': 20}, 'robot gets the inspection plan with site levels')
check(requests.get(f'{API}/api/v1/robot/routes/{route_id}/inspection', headers=oh).status_code == 403,
      'plan of another robot refused')


def upload(point_id, headers=None, uid=None, images=True, **reading):
    """A robot's inspection upload: multipart meta and, unless told otherwise, a thermal and a visual image."""
    m = {'uid': uid or str(uuid.uuid4()), 'point_id': point_id, 'ts': time.time(), **reading}
    files = {'meta': (None, json.dumps(m))}
    if images:
        files.update({'thermal': ('thermal.jpg', JPEG, 'image/jpeg'), 'visual': ('visual.jpg', JPEG, 'image/jpeg')})
    return requests.post(f'{API}/api/v1/robot/inspections', headers=headers or rh, files=files)


def readings(r, s_, t, body):
    return [{'key': 'r', 'max_c': r}, {'key': 's', 'max_c': s_}, {'key': 't', 'max_c': t}, {'key': 'body', 'max_c': body}]


# A normal capture: priority 4 at most opens nothing
r = upload(thermal['id'], ambient_c=30, thermal=readings(40, 40.5, 41, 38))
check(r.status_code == 201 and r.json()['created'] and r.json()['priority'] == 4, 'normal capture rated priority 4')
check(s.get(f'{SUB}/findings', params={'point_id': thermal['id']}).json() == [], 'no finding below priority 3')
check(upload(thermal['id'], headers=oh, thermal=readings(1, 1, 1, 1)).status_code == 403,
      'point of another robot refused')
check(upload(thermal['id'], pd={'db': 3, 'detected': True}).status_code == 400, 'pd reading on a thermal point refused')

# A patrol, and a hot phase T during it
patrol_id = s.post(f'{API}/api/v1/patrols', json={'robot_id': robot_id, 'route_id': route_id}).json()['id']
time.sleep(0.5)
cmd = next(m for m in inbox if m.get('type') == 'command' and m['name'] == 'start_patrol')
ws.send(json.dumps({'type': 'ack', 'id': cmd['id'], 'ok': True}))
uid = str(uuid.uuid4())
for _ in range(2):  # the second upload repeats the first
    r = upload(thermal['id'], uid=uid, patrol_id=patrol_id, pose={'x': 10, 'y': 0.1, 'yaw': 1.57}, ambient_c=31,
               thermal=readings(40.2, 40.9, 58.3, 44))
check(r.status_code == 201 and not r.json()['created'], 'repeated upload kept once')
hot_id = r.json()['id']
found = {x['target_key']: x for x in s.get(f'{SUB}/findings', params={'status': 'active', 'point_id': thermal['id']}).json()}
check(sorted(found) == ['body', 't'], 'findings opened for phase T and the body')
f = found['t']
check(f['target_key'] == 't' and f['priority'] == 1 and f['basis'] == 'similar' and f['last_delta_c'] == 18.1
      and f['last_value'] == 58.3 and f['asset_tag'] == tag and f['occurrences'] == 1 and f['status'] == 'open',
      'phase T 18.1 °C over phase R: priority 1 on the disconnector')
detail = s.get(f'{SUB}/inspections/{hot_id}').json()
body = next(x for x in detail['thermal'] if x['key'] == 'body')
check(detail['priority'] == 1 and detail['patrol_id'] == patrol_id and detail['images'] == ['thermal', 'visual']
      and body['delta_ambient_c'] == 13 and body['priority'] == 3 and body['basis'] == 'ambient',
      'inspection keeps every rating (body 13 °C over ambient: priority 3)')
img = s.get(f'{SUB}/inspections/{hot_id}/image/thermal')
check(img.status_code == 200 and img.headers['Content-Type'] == 'image/jpeg' and img.content == JPEG, 'thermal image served')
check(s.get(f'{SUB}/inspections/{hot_id}/image/acoustic').status_code == 404, 'missing image is 404')
check(found['body']['priority'] == 3 and found['body']['basis'] == 'ambient' and found['body']['asset_id'] == pms['id'],
      "body finding: priority 3 over ambient, on the point's asset")
check(len(s.get(f'{SUB}/findings', params={'point_id': thermal['id'], 'priority': 1}).json()) == 1,
      'findings filtered by priority')

# Seen again, less hot: the finding keeps its priority
upload(thermal['id'], patrol_id=patrol_id, ambient_c=31, thermal=readings(40, 40.5, 50, 40))
f = s.get(f"{SUB}/findings/{f['id']}").json()
check(f['priority'] == 1 and f['last_priority'] == 3 and f['occurrences'] == 2 and f['last_delta_c'] == 10,
      'seen again at priority 3, stays priority 1')
b = s.get(f"{SUB}/findings/{found['body']['id']}").json()
check(b['last_priority'] == 4 and b['occurrences'] == 1 and b['status'] == 'open',
      'body back under the finding priority: noted, still open')
r = s.post(f"{SUB}/findings/{f['id']}/acknowledge", json={'note': 'WO-2211 hot clamp'})
check(r.status_code == 200 and r.json()['status'] == 'acknowledged' and r.json()['acknowledged_by'] == USER,
      'operator acknowledges')
check(s.post(f"{SUB}/findings/{f['id']}/acknowledge").status_code == 409, 'acknowledged twice refused')

# Partial discharge: 15 dB over the point's watch level opens priority 3, 25 dB over the alarm escalates
upload(pd['id'], patrol_id=patrol_id, images=False, pd={'db': 15, 'detected': True, 'kind': 'corona'})
pf = s.get(f'{SUB}/findings', params={'point_id': pd['id']}).json()[0]
check(pf['priority'] == 3 and pf['kind'] == 'pd' and pf['asset_tag'] == trf['tag'], 'pd 15 dB: priority 3 finding')
s.post(f"{SUB}/findings/{pf['id']}/acknowledge")
upload(pd['id'], pd={'db': 25, 'detected': True})
pf = s.get(f"{SUB}/findings/{pf['id']}").json()
check(pf['priority'] == 2 and pf['status'] == 'open' and pf['occurrences'] == 2, 'pd 25 dB escalates to 2 and reopens')

# Resolving, and a new finding after it
r = s.post(f"{SUB}/findings/{f['id']}/resolve", json={'note': 'clamp retightened'})
check(r.status_code == 200 and r.json()['status'] == 'resolved' and r.json()['note'] == 'clamp retightened', 'resolved')
upload(thermal['id'], ambient_c=30, thermal=readings(40, 40, 57, 40))
again = s.get(f'{SUB}/findings', params={'status': 'open', 'point_id': thermal['id'], 'priority': 1}).json()
check(len(again) == 1 and again[0]['id'] != f['id'], 'hot again after resolving: a new finding')

# Overview, assets and trend
summary = s.get(f'{SUB}/summary').json()
check(summary['findings'].get('1', 0) >= 1 and summary['inspections_24h'] >= 5 and summary['points'] >= 2,
      'summary counts findings and inspections')
assets = {a['id']: a for a in s.get(f'{SUB}/assets').json()}
check(assets[pms['id']]['priority'] == 1 and assets[trf['id']]['priority'] == 2 and assets[pms['id']]['last_inspected'],
      'assets carry their condition')
trend = s.get(f"{SUB}/points/{thermal['id']}/trend").json()
check(len(trend['samples']) == 4 and [x['key'] for x in trend['series']] == ['r', 's', 't', 'body']
      and trend['samples'][1]['values']['t'] == 58.3, 'trend of each target')

# The patrol: coverage, report, exports
recap = s.get(f'{API}/api/v1/patrols/{patrol_id}').json()['recap']['inspections']
check(recap['planned'] == 2 and recap['done'] == 2 and recap['priority'] == 1 and recap['missed'] == [],
      'patrol covered both points')
pdf = s.get(f'{API}/api/v1/reports/patrols/{patrol_id}/pdf')
check(pdf.status_code == 200 and pdf.content[:4] == b'%PDF', 'patrol report with inspections')
for kind in ('findings', 'inspections'):
    x = s.get(f'{API}/api/v1/reports/substation/{kind}', params={'lang': 'id'})
    check(x.status_code == 200 and x.content[:2] == b'PK', f'{kind} Excel export')

# Records keep their history
check(s.delete(f"{SUB}/assets/{pms['id']}").status_code == 409, 'asset in use cannot be deleted')
check(s.delete(f"{SUB}/points/{thermal['id']}").status_code == 409, 'point with inspections cannot be deleted')
r = s.put(f"{SUB}/points/{thermal['id']}", json={**point, 'enabled': False})
check(r.status_code == 200 and not r.json()['enabled'], 'point disabled')
plan = requests.get(f'{API}/api/v1/robot/routes/{route_id}/inspection', headers=rh).json()['points']
check([p['id'] for p in plan] == [pd['id']], 'disabled point left out of the plan')

# Take a point now: the robot gets inspect_point
def answer():
    for _ in range(50):
        c = next((m for m in inbox if m.get('type') == 'command' and m['name'] == 'inspect_point'), None)
        if c:
            ws.send(json.dumps({'type': 'ack', 'id': c['id'], 'ok': True, 'msg': 'taking it'}))
            return
        time.sleep(0.1)


threading.Thread(target=answer, daemon=True).start()
r = s.post(f"{SUB}/points/{pd['id']}/inspect")
cmd = next(m for m in inbox if m.get('type') == 'command' and m['name'] == 'inspect_point')
check(r.status_code == 200 and cmd['args']['point']['id'] == pd['id'] and cmd['args']['point']['kind'] == 'pd',
      'inspect now sends the point to its robot')

time.sleep(0.3)
kinds = {(m['type'], m.get('event')) for m in live}
check(('inspection', None) in kinds and ('finding', 'opened') in kinds and ('finding', 'escalated') in kinds
      and ('finding', 'resolved') in kinds, 'dashboards hear inspections and findings')
ws.close()
dash.close()
for rid in (robot_id, other_id):
    s.delete(f'{API}/api/v1/robots/{rid}')
print('substation smoke test passed')
