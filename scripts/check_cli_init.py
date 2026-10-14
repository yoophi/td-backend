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
                def post(path, body, revision=None, method="POST", session_override=None):
                    request = urllib.request.Request(endpoint + path,
                        data=None if body is None else json.dumps(body).encode(), method=method, headers={
                            'Authorization': 'Bearer ' + token,
                            'Content-Type': 'application/json',
                            'Idempotency-Key': str(uuid.uuid4()),
                        })
                    if revision is not None:
                        request.add_header('If-Match', '"' + revision + '"')
                    if caller:
                        request.add_header('X-TD-Session', session_override or caller)
                    with urllib.request.urlopen(request, timeout=15) as response:
                        return json.load(response)['data']
                caller = ''
                base = '/v1/projects/' + project
                caller = post(base + '/sessions', {'name': 'smoke fixture',
                    'context_id': 'real-cli-show-fixture', 'agent_type': 'test'})['session']['id']
                parent = post(base + '/issues', {'title': 'Real backend parent feature',
                    'type': 'epic', 'priority': 'P1'})['issue']['id']
                child_record = post(base + '/issues', {'title': 'Real backend child task',
                    'type': 'task', 'priority': 'P2', 'parent_id': parent})['issue']
                child = child_record['id']
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
                post(base + '/issues/' + child + '/comments', {'text': 'ready for remote query'}, child_record['revision'])
                def query(expression):
                    result = subprocess.run([args.cli, '--work-dir', str(repo),
                        'query', expression, '--json'], env=env, capture_output=True,
                        text=True, timeout=30)
                    if result.returncode:
                        raise RuntimeError(result.stderr)
                    return json.loads(result.stdout)
                assert [row['id'] for row in query('type = epic')] == [parent]
                assert [row['id'] for row in query('descendant_of("' + parent + '")')] == [child]
                assert [row['id'] for row in query('comment.text ~ "ready"')] == [child]
                request = urllib.request.Request(endpoint + base + '/issues/' + child,
                    headers={'Authorization': 'Bearer ' + token, 'X-TD-Session': caller})
                with urllib.request.urlopen(request, timeout=15) as response:
                    observed = json.load(response)['data']['issue']['revision']
                post(base + '/issues/' + child + '/logs',
                    {'message': 'remote log signal', 'type': 'progress'}, observed)
                def search(text):
                    result = subprocess.run([args.cli, '--work-dir', str(repo),
                        'search', text, '--json'], env=env, capture_output=True,
                        text=True, timeout=30)
                    if result.returncode:
                        raise RuntimeError(result.stderr)
                    return json.loads(result.stdout)
                ranked = search('signal')
                assert len(ranked) == 1 and ranked[0]['Issue']['id'] == child
                assert ranked[0]['MatchField'] == 'log'
                assert search('ready') == [], 'ranked search unexpectedly included comments'
                def listing(*flags):
                    result = subprocess.run([args.cli, '--work-dir', str(repo),
                        'list', '--json', *flags], env=env, capture_output=True,
                        text=True, timeout=30)
                    if result.returncode:
                        raise RuntimeError(result.stderr)
                    return json.loads(result.stdout)
                assert len(listing()) == 2
                assert [row['id'] for row in listing('--type=epic')] == [parent]
                assert [row['id'] for row in listing('--parent=' + parent)] == [child]
                assert [row['id'] for row in listing('--epic=' + parent)] == [child]
                assert [row['id'] for row in listing('--filter=comment.text ~ "ready"')] == [child]
                assert len(listing('--sort=title', '--reverse', '--limit=1')) == 1
                result = subprocess.run([args.cli, '--work-dir', str(repo),
                    'info', '--json'], env=env, capture_output=True, text=True, timeout=30)
                if result.returncode:
                    raise RuntimeError(result.stderr)
                overview = json.loads(result.stdout)
                assert overview['store'] == 'td-backend'
                assert overview['issues']['total'] == 2
                assert overview['by_type']['epic'] == 1
                assert overview['backend']['project_id'] == project
                assert 'database' not in overview
                description_file = root / 'cli-created-description.md'
                description_file.write_text('Created through CLI\n\nPreserved paragraph\n')
                result = subprocess.run([args.cli, '--work-dir', str(repo), 'create',
                    'Remote CLI creation fixture', '--type=story', '--priority=p1',
                    '--points=3', '--labels=cli,smoke', '--minor',
                    '--description-file=' + str(description_file), '--parent=' + parent,
                    '--depends-on=' + child, '--blocks=' + parent, '--json'], env=env,
                    capture_output=True, text=True, timeout=30)
                if result.returncode:
                    raise RuntimeError(result.stderr)
                created = json.loads(result.stdout)['issue']
                assert created['type'] == 'feature' and created['priority'] == 'P1'
                assert created['points'] == 3 and created['minor']
                assert created['parent_id'] == parent
                assert created['description'] == description_file.read_text()
                created_detail = show(created['id'])
                assert created_detail['dependencies'][0]['depends_on_id'] == child
                parent_detail = show(parent)
                assert any(edge['depends_on_id'] == created['id'] for edge in parent_detail['dependencies'])
                assert parent_detail['revision'] == 'r-2'
                tombstone = post(base + '/issues', {'title': 'Deleted CLI read fixture',
                    'type': 'task', 'priority': 'P2'})['issue']
                post(base + '/issues/' + tombstone['id'], None, tombstone['revision'], method='DELETE')
                result = subprocess.run([args.cli, '--work-dir', str(repo),
                    'deleted', '--json'], env=env, capture_output=True, text=True, timeout=30)
                if result.returncode:
                    raise RuntimeError(result.stderr)
                deleted = json.loads(result.stdout)
                assert len(deleted) == 1 and deleted[0]['id'] == tombstone['id']
                assert deleted[0]['deleted_at']
                def cli_deletion(action, issue_id, reason):
                    result = subprocess.run([args.cli, '--work-dir', str(repo),
                        action, issue_id, '--reason=' + reason, '--json'], env=env,
                        capture_output=True, text=True, timeout=30)
                    if result.returncode:
                        raise RuntimeError(f"{action} exit {result.returncode}: stdout={result.stdout!r}, stderr={result.stderr!r}")
                    return json.loads(result.stdout)
                restored = cli_deletion('restore', tombstone['id'], 'smoke restore')
                assert restored[0]['id'] == tombstone['id'] and not restored[0]['issue'].get('deleted_at')
                assert cli_deletion('restore', tombstone['id'], 'smoke restore')[0]['noop']
                assert any(log['message'] == 'Restored: smoke restore' for log in show(tombstone['id'])['logs'])
                removed = cli_deletion('delete', tombstone['id'], 'smoke delete')
                assert removed[0]['issue']['deleted_at']
                assert cli_deletion('delete', tombstone['id'], 'smoke delete')[0]['noop']
                another = post(base + '/issues', {'title': 'Partial deletion fixture',
                    'type': 'task'})['issue']
                partial_args = [args.cli, '--work-dir', str(repo), 'delete',
                    another['id'], 'td-ffffffffffffffff', '--reason=partial smoke', '--json']
                for attempt in range(2):
                    partial = subprocess.run(partial_args, env=env, capture_output=True,
                        text=True, timeout=30)
                    assert partial.returncode != 0 and 'completed for ' + another['id'] in partial.stdout
                listed = subprocess.run([args.cli, '--work-dir', str(repo),
                    'deleted', '--json'], env=env, capture_output=True, text=True, timeout=30)
                assert listed.returncode == 0
                assert {row['id'] for row in json.loads(listed.stdout)} == {tombstone['id'], another['id']}
                cli_deletion('restore', another['id'], 'resume smoke')
                assert sum(log['message'] == 'Deleted: partial smoke'
                    for log in show(another['id'])['logs']) == 1
                def cli_status(value):
                    result = subprocess.run([args.cli, '--work-dir', str(repo),
                        'update', another['id'], '--status=' + value, '--json'],
                        env=env, capture_output=True, text=True, timeout=30)
                    if result.returncode:
                        raise RuntimeError(result.stdout + result.stderr)
                    return json.loads(result.stdout)
                assert cli_status('in_progress')['issue']['status'] == 'in_progress'
                assert cli_status('in_progress')['noop']
                assert show(another['id'])['status'] == 'in_progress'
                guarded = subprocess.run([args.cli, '--work-dir', str(repo),
                    'update', another['id'], '--status=in_review', '--json'],
                    env=env, capture_output=True, text=True, timeout=30)
                assert guarded.returncode != 0
                assert show(another['id'])['status'] == 'in_progress'
                mixed = subprocess.run([args.cli, '--work-dir', str(repo),
                    'update', another['id'], '--status=open', '--title=must-not-change',
                    '--json'], env=env, capture_output=True, text=True, timeout=30)
                assert mixed.returncode != 0
                assert show(another['id'])['status'] == 'in_progress'
                assert show(another['id'])['title'] != 'must-not-change'
                def cli_patch(*flags):
                    result = subprocess.run([args.cli, '--work-dir', str(repo),
                        'update', another['id'], *flags, '--json'], env=env,
                        capture_output=True, text=True, timeout=30)
                    if result.returncode:
                        raise RuntimeError(result.stdout + result.stderr)
                    return json.loads(result.stdout)
                patch_flags = ('--title=Updated remote fixture',
                    '--description=Initial text', '--priority=P1', '--points=5',
                    '--labels=backend,smoke', '--due=2026-12-31', '--parent=' + parent)
                updated = cli_patch(*patch_flags)
                assert updated['issue']['title'] == 'Updated remote fixture'
                assert updated['issue']['description'] == 'Initial text'
                assert updated['issue']['priority'] == 'P1' and updated['issue']['points'] == 5
                assert updated['issue']['parent_id'] == parent
                assert cli_patch(*patch_flags)['noop']
                appended = cli_patch('--append', '--description=Second text')
                assert appended['issue']['description'] == 'Initial text\n\nSecond text'
                partial_patch = [args.cli, '--work-dir', str(repo), 'update',
                    another['id'], 'td-eeeeeeeeeeeeeeee',
                    '--title=Partially updated fixture', '--json']
                first = subprocess.run(partial_patch, env=env, capture_output=True,
                    text=True, timeout=30)
                assert first.returncode != 0 and 'completed for ' + another['id'] in first.stdout
                revision_after_partial = show(another['id'])['revision']
                second = subprocess.run(partial_patch, env=env, capture_output=True,
                    text=True, timeout=30)
                assert second.returncode != 0 and 'completed for ' + another['id'] in second.stdout
                assert show(another['id'])['revision'] == revision_after_partial
                work = post(base + '/issues', {'title': 'Workflow CLI fixture',
                    'type': 'task'})['issue']
                def cli_workflow(action):
                    result = subprocess.run([args.cli, '--work-dir', str(repo),
                        action, work['id'], '--reason=workflow smoke', '--json'],
                        env=env, capture_output=True, text=True, timeout=30)
                    if result.returncode:
                        raise RuntimeError(result.stdout + result.stderr)
                    return json.loads(result.stdout)
                started = cli_workflow('start')
                assert started['status'] == 'in_progress'
                assert started['issue']['implementer_session']
                assert cli_workflow('start')['noop']
                unstarted = cli_workflow('unstart')
                assert unstarted['status'] == 'open'
                assert not unstarted['issue'].get('implementer_session')
                assert cli_workflow('unstart')['noop']
                assert show(work['id'])['status'] == 'open'
                blocked = post(base + '/issues', {'title': 'Blocked start CLI fixture',
                    'type': 'task'})['issue']
                block = subprocess.run([args.cli, '--work-dir', str(repo), 'update',
                    blocked['id'], '--status=blocked', '--json'], env=env,
                    capture_output=True, text=True, timeout=30)
                assert block.returncode == 0
                ordinary_start = subprocess.run([args.cli, '--work-dir', str(repo),
                    'start', blocked['id'], '--json'], env=env,
                    capture_output=True, text=True, timeout=30)
                assert ordinary_start.returncode != 0
                forced_start = subprocess.run([args.cli, '--work-dir', str(repo),
                    'start', blocked['id'], '--force', '--json'], env=env,
                    capture_output=True, text=True, timeout=30)
                assert forced_start.returncode == 0
                assert json.loads(forced_start.stdout)['status'] == 'in_progress'
                def cli_direct(action, issue_id):
                    result = subprocess.run([args.cli, '--work-dir', str(repo),
                        action, issue_id, '--reason=direct workflow smoke', '--json'],
                        env=env, capture_output=True, text=True, timeout=30)
                    if result.returncode:
                        raise RuntimeError(result.stdout + result.stderr)
                    return json.loads(result.stdout)
                assert cli_direct('block', work['id'])['status'] == 'blocked'
                assert cli_direct('block', work['id'])['noop']
                assert cli_direct('unblock', work['id'])['status'] == 'open'
                assert cli_direct('unblock', work['id'])['noop']
                ready_to_close = show(work['id'])
                closed = post(base + '/issues/' + work['id'] + '/close', {'admin_reason':'smoke fixture'},
                    ready_to_close['revision'])['issue']
                assert closed['status'] == 'closed'
                assert cli_direct('reopen', work['id'])['status'] == 'open'
                assert cli_direct('reopen', work['id'])['noop']
                def cli_comment(*tail):
                    result = subprocess.run([args.cli, '--work-dir', str(repo),
                        *tail, '--json'], env=env, capture_output=True,
                        text=True, timeout=30)
                    if result.returncode:
                        raise RuntimeError(result.stdout + result.stderr)
                    return json.loads(result.stdout)
                comment = cli_comment('comment', work['id'], 'Remote CLI comment fixture')
                assert comment['comment']['text'] == 'Remote CLI comment fixture'
                assert comment['comment']['issue_id'] == work['id']
                cli_comment('comments', 'add', work['id'], 'Second CLI comment')
                comments = cli_comment('comments', work['id'])
                assert [item['text'] for item in comments] == [
                    'Remote CLI comment fixture', 'Second CLI comment']
                own_work_session = post(base + '/work-sessions',
                    {'name':'CLI log ownership fixture'},
                    session_override=started['issue']['implementer_session'])['work_session']['id']
                logged = cli_comment('log', work['id'],
                    'Remote CLI decision log', '--type=decision')
                assert logged['log']['message'] == 'Remote CLI decision log'
                assert logged['log']['type'] == 'decision'
                assert logged['log']['timestamp']
                assert logged['log']['work_session_id'] == own_work_session
                assert any(log['message'] == 'Remote CLI decision log'
                    for log in show(work['id'])['logs'])
                handoff = cli_comment('handoff', work['id'],
                    '--done=CLI implementation complete',
                    '--remaining=Review final behavior',
                    '--decision=Use remote activity')
                assert handoff['activity']['kind'] == 'handoff'
                assert handoff['activity']['done'] == ['CLI implementation complete']
                assert handoff['activity']['work_session_id'] == own_work_session
                assert handoff['activity']['snapshot']['branch']
                assert show(work['id'])['handoff']['done'] == ['CLI implementation complete']
                review_after_handoff = subprocess.run([args.cli,
                    '--work-dir', str(repo), 'update', work['id'],
                    '--status=in_review', '--json'], env=env, capture_output=True,
                    text=True, timeout=30)
                assert review_after_handoff.returncode == 0
                assert json.loads(review_after_handoff.stdout)['issue']['status'] == 'in_review'
                for directory in (repo, attached):
                    assert not (directory / '.todos/issues.db').exists(), 'local SQLite fallback'
                    config = (directory / '.todos/config.json').read_text()
                    assert token not in config, 'token persisted in config'
                print('PASS: real backend init/reuse/worktree/attach, show/prefix/children/tree/multiple, TDQ fields/hierarchy/comments, ranked log search, list filters/TDQ, info statistics, CLI create/dependency/blocks, deleted listing/write/reason, workflow status/field update/start/unstart/block/unblock/reopen, comments/log/handoff, no local DB/token')
            finally:
                server.terminate()
                try:
                    server.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait()


if __name__ == '__main__':
    main()
