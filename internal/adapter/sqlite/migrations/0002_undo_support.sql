-- Preserve created work sessions for audit when their creation is undone.
ALTER TABLE work_sessions ADD COLUMN deleted_at TEXT;
CREATE INDEX work_sessions_live ON work_sessions(project_id, started_at) WHERE deleted_at IS NULL;

CREATE TABLE operation_undos (
    project_id TEXT NOT NULL,
    operation_row_id TEXT NOT NULL,
    original_row_id TEXT NOT NULL,
    reason TEXT NOT NULL CHECK (length(trim(reason)) > 0),
    PRIMARY KEY (project_id, operation_row_id),
    UNIQUE (project_id, original_row_id),
    FOREIGN KEY (project_id, operation_row_id) REFERENCES operations(project_id, row_id) DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (project_id, original_row_id) REFERENCES operations(project_id, row_id)
);
