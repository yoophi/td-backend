#!/usr/bin/env python3
"""Validate the SQLite design draft, not the unimplemented Go adapter."""
import sqlite3
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
NOW = '2026-10-13T00:00:00Z'

def reject(db, sql, params=()):
    try:
        db.execute(sql, params)
    except sqlite3.IntegrityError:
        return
    raise AssertionError('Constraint did not reject: ' + sql)

with tempfile.TemporaryDirectory(prefix='td-backend-schema-') as directory:
    filename = str(Path(directory) / 'design.db')
    db = sqlite3.connect(filename, isolation_level=None)
    db.execute('PRAGMA journal_mode=WAL')
    db.executescript((ROOT / 'docs/sqlite-schema.sql').read_text())
    db.execute('INSERT INTO actors(id,name,created_at) VALUES(?,?,?)', ('a', 'Test actor', NOW))
    for project in ['p', 'q']:
        db.execute('INSERT INTO projects(id,name,created_at) VALUES(?,?,?)', (project, 'Same display name', NOW))
        db.execute('INSERT INTO project_memberships VALUES(?,?,?,?)', (project, 'a', 'owner', NOW))
        db.execute('INSERT INTO sessions(project_id,id,actor_id,context_id,started_at,last_activity) VALUES(?,?,?,?,?,?)', (project, 's', 'a', 'context', NOW, NOW))
        db.execute('INSERT INTO issues(project_id,id,title,creator_session,created_at,updated_at) VALUES(?,?,?,?,?,?)', (project, project + '-issue', 'Test issue title', 's', NOW, NOW))
    reject(db, 'UPDATE issues SET parent_id=? WHERE project_id=?', ('q-issue', 'p'))
    reject(db, 'INSERT INTO issue_dependencies VALUES(?,?,?,?)', ('p', 'cross-project', 'p-issue', 'q-issue'))
    reject(db, 'INSERT INTO issue_dependencies VALUES(?,?,?,?)', ('p', 'self-edge', 'p-issue', 'p-issue'))
    reject(db, 'INSERT INTO session_focus(project_id,session_id,issue_id) VALUES(?,?,?)', ('p', 's', 'q-issue'))
    reject(db, 'UPDATE issues SET priority=? WHERE project_id=?', ('P9', 'p'))
    reject(db, 'UPDATE issues SET points=? WHERE project_id=?', (4, 'p'))

    operation_sql = '''INSERT INTO operations(row_id,project_id,actor_id,session_id,
        scope_key,operation_id,fingerprint,command_kind,request_method,request_path,
        representation_version,response_status,response_headers,response_body,created_at)
        VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)'''
    operation = ('row-1','p','a','s','p','key-1',b'fingerprint','issue.create','POST',
                 '/issues',1,201,'{"ETag":"r-1"}',b'{"ok":true,"data":{}}',NOW)
    db.execute(operation_sql, operation)
    reject(db, operation_sql, ('row-2',) + operation[1:])
    reject(db, operation_sql, ('row-wrong','p','a','s','q') + operation[5:])

    review_sql = '''INSERT INTO issue_reviews(project_id,id,issue_id,reviewer_session,
        decision,basis_content_version,operation_row_id,created_at) VALUES(?,?,?,?,?,?,?,?)'''
    db.execute(review_sql, ('p','review-1','p-issue','s','approved',1,'row-1',NOW))
    reject(db, review_sql, ('p','review-2','p-issue','s','approved',1,'row-1',NOW))
    db.execute('UPDATE issue_reviews SET superseded_at=? WHERE project_id=? AND id=?', (NOW,'p','review-1'))
    db.execute(review_sql, ('p','review-2','p-issue','s','changes_requested',1,'row-1',NOW))

    # Deferred receipt FK fails at commit; every preceding aggregate write rolls back.
    revision = db.execute('SELECT revision FROM issues WHERE project_id=?', ('p',)).fetchone()[0]
    db.execute('BEGIN IMMEDIATE')
    db.execute('UPDATE issues SET revision=revision+1 WHERE project_id=?', ('p',))
    db.execute('''INSERT INTO activities(project_id,id,issue_id,session_id,operation_row_id,
        kind,message,log_type,content_version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)''',
        ('p','orphan-log','p-issue','s','missing-receipt','log','progress','progress',1,NOW,NOW))
    try:
        db.execute('COMMIT')
    except sqlite3.IntegrityError:
        db.execute('ROLLBACK')
    else:
        raise AssertionError('Deferred receipt constraint did not reject commit')
    assert db.execute('SELECT revision FROM issues WHERE project_id=?', ('p',)).fetchone()[0] == revision
    assert db.execute('SELECT count(*) FROM activities').fetchone()[0] == 0

    # Separate connection retains its read snapshot while the writer commits in WAL.
    reader = sqlite3.connect(filename, isolation_level=None)
    reader.execute('PRAGMA foreign_keys=ON')
    reader.execute('BEGIN')
    before = reader.execute('SELECT revision FROM issues WHERE project_id=?', ('p',)).fetchone()[0]
    db.execute('UPDATE issues SET revision=revision+1 WHERE project_id=?', ('p',))
    assert reader.execute('SELECT revision FROM issues WHERE project_id=?', ('p',)).fetchone()[0] == before
    reader.execute('COMMIT')
    assert reader.execute('SELECT revision FROM issues WHERE project_id=?', ('p',)).fetchone()[0] == before + 1
    assert db.execute('UPDATE issues SET revision=revision+1 WHERE project_id=? AND revision=?', ('p',before)).rowcount == 0
    assert db.execute('PRAGMA foreign_key_check').fetchall() == []
    tables = db.execute("SELECT count(*) FROM sqlite_master WHERE type='table'").fetchone()[0]
    reader.close()
    db.close()
    persisted = sqlite3.connect(filename)
    assert persisted.execute('SELECT response_body FROM operations WHERE row_id=?', ('row-1',)).fetchone()[0] == operation[13]
    persisted.close()
    print(f'SQLite {sqlite3.sqlite_version}: {tables} tables; project isolation, enum/points, receipt uniqueness, active review, atomic rollback, WAL snapshot, stale revision and persisted receipt verified')
