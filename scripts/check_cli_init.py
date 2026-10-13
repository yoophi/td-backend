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
                for directory in (repo, attached):
                    assert not (directory / '.todos/issues.db').exists(), 'local SQLite fallback'
                    config = (directory / '.todos/config.json').read_text()
                    assert token not in config, 'token persisted in config'
                print('PASS: real backend create, repeat init, worktree reuse, explicit attach, conflict validation, no local DB/token')
            finally:
                server.terminate()
                try:
                    server.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait()


if __name__ == '__main__':
    main()
