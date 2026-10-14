#!/usr/bin/env python3
"""Exercise CLI project creation/reuse/attachment against a real backend.

Build both binaries, then pass --backend and --cli absolute paths.
All server data and Git worktrees are temporary; no installed CLI is changed.
"""
import argparse
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--backend', required=True)
    parser.add_argument('--cli', required=True)
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='td-backend-cli-init-') as temp:
        root = Path(temp)
        repo = root / 'project'
        repo.mkdir()
        def git(*arguments):
            subprocess.run(['git', '-C', str(repo), *arguments], check=True,
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        git('init', '-q')
        git('-c', 'user.name=Init Test', '-c', 'user.email=init@example.test',
            'commit', '--allow-empty', '-qm', 'test fixture')
        token = secrets.token_hex(32)
        env = dict(os.environ, TD_BACKEND_BOOTSTRAP_TOKEN=token, TD_BACKEND_TOKEN=token)
        env["TD_BACKEND_CONTEXT_ROOT"] = str(root / "caller-contexts")
        env["TD_SESSION_ID"] = "real-backend-cli-smoke"
        env.pop('TD_FEATURE_SYNC_AUTOSYNC', None)
        env.pop('TD_FEATURE_SYNC_CLI', None)
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        endpoint = f'http://127.0.0.1:{port}'
        with (root / 'server.log').open('w') as log:
            server = subprocess.Popen([args.backend, '--addr', f'127.0.0.1:{port}',
                                       '--db', str(root / 'server.db')], env=env,
                                      stdout=log, stderr=log)
            try:
                deadline = time.monotonic() + 15
                while True:
                    if server.poll() is not None:
                        raise RuntimeError('backend exited before readiness')
                    try:
                        with urllib.request.urlopen(endpoint + '/healthz', timeout=1) as response:
                            assert json.load(response)['ok']
                        break
                    except OSError:
                        if time.monotonic() >= deadline:
                            raise RuntimeError('backend readiness timed out')
                        time.sleep(.1)
                def init(directory, *flags, success=True):
                    result = subprocess.run([args.cli, '--work-dir', str(directory),
                                             'init', '--json', *flags], env=env,
                                            capture_output=True, text=True, timeout=30)
                    if success:
                        if result.returncode:
                            raise RuntimeError(result.stderr)
                        return json.loads(result.stdout)
                    assert result.returncode != 0, 'invalid association unexpectedly succeeded'
                first = init(repo, '--store=td-backend,' + endpoint)
                project = first['backend']['project_id']
                assert first['store'] == 'td-backend'
                assert init(repo)['backend']['project_id'] == project
                worktree = root / 'worktree'
                git('worktree', 'add', '-qb', 'test-worktree', str(worktree))
                assert init(worktree)['backend']['project_id'] == project
                assert not (worktree / '.todos/config.json').exists(), 'worktree association forked'
                attached = root / 'attached'
                attached.mkdir()
                assert init(attached, '--store=td-backend,' + endpoint,
                            '--project-id=' + project)['backend']['project_id'] == project
                init(repo, '--project-id=prj_0199dc38-1200-7000-8000-000000000001', success=False)
                init(repo, '--project-id=invalid', success=False)
                def post(path, body):
                    request = urllib.request.Request(endpoint + path,
                        data=json.dumps(body).encode(), method='POST', headers={
                            'Authorization': 'Bearer ' + token,
                            'Content-Type': 'application/json',
                            'Idempotency-Key': str(uuid.uuid4()),
                        })
                    if caller:
                        request.add_header('X-TD-Session', caller)
                    with urllib.request.urlopen(request, timeout=15) as response:
                        return json.load(response)['data']
                caller = ''
                base = '/v1/projects/' + project
                caller = post(base + '/sessions', {'name': 'smoke fixture',
                    'context_id': 'real-cli-show-fixture', 'agent_type': 'test'})['session']['id']
                parent = post(base + '/issues', {'title': 'Real backend parent feature',
                    'type': 'epic', 'priority': 'P1'})['issue']['id']
                child = post(base + '/issues', {'title': 'Real backend child task',
                    'type': 'task', 'priority': 'P2', 'parent_id': parent})['issue']['id']
                def show(*flags):
                    result = subprocess.run([args.cli, '--work-dir', str(repo),
                        'show', '--json', *flags], env=env, capture_output=True,
                        text=True, timeout=30)
                    if result.returncode:
                        raise RuntimeError(result.stderr)
                    return json.loads(result.stdout)
                detail = show(parent)
                assert detail['id'] == parent and detail['revision'].startswith('r-')
                assert show(parent[:10])['id'] == parent
                assert show(parent, '--children')['children'][0]['id'] == child
                assert show(parent, '--tree')['children'][0]['id'] == child
                assert len(show(parent, child)) == 2
                for directory in (repo, attached):
                    assert not (directory / '.todos/issues.db').exists(), 'local SQLite fallback'
                    config = (directory / '.todos/config.json').read_text()
                    assert token not in config, 'token persisted in config'
                print('PASS: real backend init/reuse/worktree/attach, show/prefix/children/tree/multiple, no local DB/token')
            finally:
                server.terminate()
                try:
                    server.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait()


if __name__ == '__main__':
    main()
