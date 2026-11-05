#!/usr/bin/env python3
"""First-run setup of a netra-api on an empty database: installation ID, first admin, bound license.

Needs the license tool and a key the server trusts (pip install requests):
NETRA_URL, NETRA_LICENSE_TOOL (./netra-license), NETRA_LICENSE_KEY (the .key file).
"""
import os
import subprocess
import sys

import requests

API = os.environ.get('NETRA_URL', 'http://localhost:8080')
TOOL = os.environ.get('NETRA_LICENSE_TOOL', './netra-license')
KEY = os.environ['NETRA_LICENSE_KEY']
SETUP = f'{API}/api/v1/setup'


def check(cond, what):
    """Fail loudly."""
    print(('ok   ' if cond else 'FAIL ') + what)
    if not cond:
        sys.exit(1)


def issue(installation):
    """A substation license bound to an installation."""
    out = subprocess.run([TOOL, 'issue', '-key', KEY, '-client', 'Setup test', '-edition', 'substation', '-tier',
                          'basic', '-days', '1', '-installation', installation], check=True, capture_output=True,
                         text=True)
    return out.stdout.strip()


state = requests.get(SETUP).json()
installation = state['installation_id']
check(state['required'] is True and installation.startswith('NETRA-'), f'fresh server asks for setup ({installation})')
check(requests.post(f'{API}/api/v1/auth/login', json={'username': 'admin', 'password': 'x'}).status_code == 401,
      'no account before setup')

r = requests.post(SETUP, json={'username': 'admin', 'password': 'admin-pass', 'license': issue('NETRA-0000-0000-0000')})
check(r.status_code == 400 and 'installation' in r.json()['error'], 'license for another installation refused')
check(requests.get(SETUP).json()['required'] is True, 'refused setup created nothing')
check(requests.post(SETUP, json={'username': 'admin', 'password': 'short'}).status_code == 400, 'short password refused')

r = requests.post(SETUP, json={'username': 'admin', 'display_name': 'Administrator', 'password': 'admin-pass',
                               'license': issue(installation.lower())})
check(r.status_code == 201 and r.json()['role'] == 'admin' and r.json()['token'], 'setup created the admin')
auth = {'Authorization': f"Bearer {r.json()['token']}"}
info = requests.get(f'{API}/api/v1/info', headers=auth).json()
check(info['license']['status'] == 'valid' and info['license']['installation'] == installation, 'bound license installed')
check(info['map_mode'] == 'gps' and 'substation' in info['features'], 'substation edition: GPS map, substation module')

check(requests.post(SETUP, json={'username': 'second', 'password': 'second-pass'}).status_code == 409,
      'setup closes after the first admin')
check(requests.get(SETUP).json()['required'] is False, 'setup no longer required')
login = requests.post(f'{API}/api/v1/auth/login', json={'username': 'admin', 'password': 'admin-pass'})
check(login.status_code == 200, 'the new admin signs in')
print('setup smoke test passed')
