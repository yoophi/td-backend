CREATE TABLE work_session_logs (
    project_id TEXT NOT NULL,
    id TEXT NOT NULL,
    work_session_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    operation_row_id TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK (revision >= 1),
    message TEXT NOT NULL CHECK (length(trim(message)) > 0),
    log_type TEXT NOT NULL CHECK (log_type IN
        ('progress', 'security', 'blocker', 'decision', 'hypothesis', 'tried', 'result', 'orchestration')),
    created_at TEXT NOT NULL,
    deleted_at TEXT,
    PRIMARY KEY (project_id, id),
    FOREIGN KEY (project_id, work_session_id) REFERENCES work_sessions(project_id, id),
    FOREIGN KEY (project_id, session_id) REFERENCES sessions(project_id, id),
    FOREIGN KEY (project_id, operation_row_id) REFERENCES operations(project_id, row_id)
        DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX work_session_logs_history ON work_session_logs(project_id, work_session_id, created_at, id)
WHERE deleted_at IS NULL;

CREATE TABLE operation_effects_new (
    project_id TEXT NOT NULL,
    operation_row_id TEXT NOT NULL,
    entity_kind TEXT NOT NULL CHECK (entity_kind IN
        ('project', 'session', 'issue', 'board', 'note', 'focus', 'work_session', 'work_session_log')),
    entity_id TEXT NOT NULL,
    after_revision TEXT NOT NULL,
    state_version INTEGER NOT NULL CHECK (state_version >= 1),
    before_json TEXT CHECK (before_json IS NULL OR json_valid(before_json)),
    after_json TEXT NOT NULL CHECK (json_valid(after_json)),
    PRIMARY KEY (project_id, operation_row_id, entity_kind, entity_id),
    FOREIGN KEY (project_id, operation_row_id) REFERENCES operations(project_id, row_id)
        DEFERRABLE INITIALLY DEFERRED
);
INSERT INTO operation_effects_new SELECT * FROM operation_effects;
DROP TABLE operation_effects;
ALTER TABLE operation_effects_new RENAME TO operation_effects;
CREATE INDEX effects_entity ON operation_effects(project_id, entity_kind, entity_id);
