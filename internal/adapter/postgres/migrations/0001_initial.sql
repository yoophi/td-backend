-- PostgreSQL schema corresponding to the four shipped SQLite migrations.
CREATE TABLE actors (
    id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    can_create_project INTEGER NOT NULL DEFAULT 0 CHECK (can_create_project IN (0, 1)),
    created_at TEXT NOT NULL
);

CREATE TABLE access_tokens (
    id TEXT PRIMARY KEY NOT NULL,
    actor_id TEXT NOT NULL REFERENCES actors(id),
    token_digest BYTEA NOT NULL UNIQUE,
    created_at TEXT NOT NULL,
    revoked_at TEXT
);

CREATE TABLE projects (
    id TEXT PRIMARY KEY NOT NULL CHECK (id <> '@server'),
    name TEXT NOT NULL,
    repository TEXT NOT NULL DEFAULT '',
    review_policy_mode TEXT NOT NULL DEFAULT 'trusted'
        CHECK (review_policy_mode IN ('trusted', 'delegated', 'strict', 'balanced')),
    title_min_length INTEGER NOT NULL DEFAULT 15 CHECK (title_min_length >= 1),
    title_max_length INTEGER NOT NULL DEFAULT 200 CHECK (title_max_length >= title_min_length),
    change_seq INTEGER NOT NULL DEFAULT 0 CHECK (change_seq >= 0),
    created_at TEXT NOT NULL
);

CREATE TABLE project_memberships (
    project_id TEXT NOT NULL REFERENCES projects(id),
    actor_id TEXT NOT NULL REFERENCES actors(id),
    role TEXT NOT NULL CHECK (role IN ('reader', 'writer', 'owner')),
    created_at TEXT NOT NULL,
    PRIMARY KEY (project_id, actor_id)
);

CREATE TABLE sessions (
    project_id TEXT NOT NULL REFERENCES projects(id),
    id TEXT NOT NULL,
    actor_id TEXT NOT NULL REFERENCES actors(id),
    name TEXT NOT NULL DEFAULT '',
    branch TEXT NOT NULL DEFAULT '',
    agent_type TEXT NOT NULL DEFAULT '',
    context_id TEXT NOT NULL,
    previous_session_id TEXT,
    started_at TEXT NOT NULL,
    last_activity TEXT NOT NULL,
    PRIMARY KEY (project_id, id),
    FOREIGN KEY (project_id, previous_session_id) REFERENCES sessions(project_id, id)
);

CREATE INDEX sessions_presence ON sessions(project_id, last_activity);

CREATE INDEX sessions_owner ON sessions(project_id, actor_id, context_id);

CREATE TABLE issues (
    project_id TEXT NOT NULL REFERENCES projects(id),
    id TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision >= 1),
    content_version INTEGER NOT NULL DEFAULT 1 CHECK (content_version >= 1),
    title TEXT NOT NULL CHECK (length(trim(title)) >= 1),
    description TEXT NOT NULL DEFAULT '',
    acceptance TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'in_progress', 'blocked', 'in_review', 'closed')),
    type TEXT NOT NULL DEFAULT 'task' CHECK (type IN ('bug', 'feature', 'task', 'epic', 'chore')),
    priority TEXT NOT NULL DEFAULT 'P2' CHECK (priority IN ('P0', 'P1', 'P2', 'P3', 'P4')),
    points INTEGER NOT NULL DEFAULT 0 CHECK (points IN (0, 1, 2, 3, 5, 8, 13, 21)),
    parent_id TEXT,
    sprint TEXT NOT NULL DEFAULT '',
    minor INTEGER NOT NULL DEFAULT 0 CHECK (minor IN (0, 1)),
    creator_session TEXT NOT NULL,
    implementer_session TEXT,
    reviewer_session TEXT,
    review_requested_by_session TEXT,
    closed_by_session TEXT,
    created_branch TEXT NOT NULL DEFAULT '',
    defer_until TEXT,
    due_date TEXT,
    defer_count INTEGER NOT NULL DEFAULT 0 CHECK (defer_count >= 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    reviewed_at TEXT,
    closed_at TEXT,
    deleted_at TEXT,
    PRIMARY KEY (project_id, id),
    CHECK (parent_id IS NULL OR parent_id <> id),
    FOREIGN KEY (project_id, parent_id) REFERENCES issues(project_id, id),
    FOREIGN KEY (project_id, creator_session) REFERENCES sessions(project_id, id),
    FOREIGN KEY (project_id, implementer_session) REFERENCES sessions(project_id, id),
    FOREIGN KEY (project_id, reviewer_session) REFERENCES sessions(project_id, id),
    FOREIGN KEY (project_id, review_requested_by_session) REFERENCES sessions(project_id, id),
    FOREIGN KEY (project_id, closed_by_session) REFERENCES sessions(project_id, id)
);

CREATE INDEX issues_live_status ON issues(project_id, status, priority, id) WHERE deleted_at IS NULL;

CREATE INDEX issues_parent ON issues(project_id, parent_id) WHERE deleted_at IS NULL;

CREATE INDEX issues_updated ON issues(project_id, updated_at, id);

CREATE TABLE issue_labels (
    project_id TEXT NOT NULL,
    issue_id TEXT NOT NULL,
    label TEXT NOT NULL,
    PRIMARY KEY (project_id, issue_id, label),
    FOREIGN KEY (project_id, issue_id) REFERENCES issues(project_id, id)
);

CREATE INDEX labels_catalog ON issue_labels(project_id, label, issue_id);

CREATE TABLE issue_dependencies (
    project_id TEXT NOT NULL,
    id TEXT NOT NULL,
    issue_id TEXT NOT NULL,
    depends_on_id TEXT NOT NULL,
    PRIMARY KEY (project_id, id),
    UNIQUE (project_id, issue_id, depends_on_id),
    CHECK (issue_id <> depends_on_id),
    FOREIGN KEY (project_id, issue_id) REFERENCES issues(project_id, id),
    FOREIGN KEY (project_id, depends_on_id) REFERENCES issues(project_id, id)
);

CREATE INDEX dependencies_reverse ON issue_dependencies(project_id, depends_on_id, issue_id);

CREATE TABLE issue_files (
    project_id TEXT NOT NULL,
    id TEXT NOT NULL,
    issue_id TEXT NOT NULL,
    file_path TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('implementation', 'test', 'reference', 'config')),
    linked_sha TEXT NOT NULL DEFAULT '',
    linked_at TEXT NOT NULL,
    PRIMARY KEY (project_id, id),
    UNIQUE (project_id, issue_id, file_path, role),
    FOREIGN KEY (project_id, issue_id) REFERENCES issues(project_id, id)
);

CREATE TABLE work_sessions (
    project_id TEXT NOT NULL,
    id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision >= 1),
    name TEXT NOT NULL,
    started_at TEXT NOT NULL,
    ended_at TEXT,
    start_sha TEXT NOT NULL DEFAULT '',
    end_sha TEXT NOT NULL DEFAULT '', deleted_at TEXT,
    PRIMARY KEY (project_id, id),
    FOREIGN KEY (project_id, session_id) REFERENCES sessions(project_id, id)
);

CREATE INDEX work_sessions_owner ON work_sessions(project_id, session_id, started_at);

CREATE TABLE work_session_issues (
    project_id TEXT NOT NULL,
    work_session_id TEXT NOT NULL,
    issue_id TEXT NOT NULL,
    tagged_at TEXT NOT NULL,
    PRIMARY KEY (project_id, work_session_id, issue_id),
    FOREIGN KEY (project_id, work_session_id) REFERENCES work_sessions(project_id, id),
    FOREIGN KEY (project_id, issue_id) REFERENCES issues(project_id, id)
);

CREATE TABLE operations (
    row_id TEXT PRIMARY KEY NOT NULL,
    project_id TEXT REFERENCES projects(id),
    actor_id TEXT NOT NULL REFERENCES actors(id),
    session_id TEXT,
    scope_key TEXT NOT NULL,
    operation_id TEXT NOT NULL,
    fingerprint BYTEA NOT NULL,
    command_kind TEXT NOT NULL,
    request_method TEXT NOT NULL,
    request_path TEXT NOT NULL,
    representation_version INTEGER NOT NULL CHECK (representation_version >= 1),
    response_status INTEGER NOT NULL CHECK (response_status BETWEEN 200 AND 299),
    response_headers TEXT NOT NULL CHECK (response_headers IS JSON),
    response_body BYTEA NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (actor_id, scope_key, operation_id),
    UNIQUE (project_id, row_id),
    CHECK (scope_key = '@server' OR (project_id IS NOT NULL AND scope_key = project_id)),
    CHECK (session_id IS NULL OR project_id IS NOT NULL),
    FOREIGN KEY (project_id, session_id) REFERENCES sessions(project_id, id)
);

CREATE INDEX operations_history ON operations(project_id, actor_id, created_at, row_id);

CREATE TABLE activities (
    project_id TEXT NOT NULL,
    id TEXT NOT NULL,
    issue_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    work_session_id TEXT,
    operation_row_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('comment', 'log', 'handoff')),
    message TEXT NOT NULL DEFAULT '',
    log_type TEXT CHECK (log_type IS NULL OR log_type IN
        ('progress', 'security', 'blocker', 'decision', 'hypothesis', 'tried', 'result', 'orchestration')),
    content_version INTEGER NOT NULL CHECK (content_version >= 1),
    payload_version INTEGER NOT NULL DEFAULT 1,
    payload_json TEXT NOT NULL DEFAULT '{}' CHECK (payload_json IS JSON),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT,
    PRIMARY KEY (project_id, id),
    UNIQUE (project_id, issue_id, id),
    CHECK (kind = 'handoff' OR length(trim(message)) > 0),
    CHECK (kind <> 'log' OR log_type IS NOT NULL),
    FOREIGN KEY (project_id, issue_id) REFERENCES issues(project_id, id),
    FOREIGN KEY (project_id, session_id) REFERENCES sessions(project_id, id),
    FOREIGN KEY (project_id, work_session_id) REFERENCES work_sessions(project_id, id),
    FOREIGN KEY (project_id, operation_row_id) REFERENCES operations(project_id, row_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX activities_issue ON activities(project_id, issue_id, created_at, id);

CREATE INDEX activities_recent ON activities(project_id, created_at, id);

CREATE TABLE issue_reviews (
    project_id TEXT NOT NULL,
    id TEXT NOT NULL,
    issue_id TEXT NOT NULL,
    reviewer_session TEXT NOT NULL,
    requested_by_session TEXT,
    decision TEXT NOT NULL CHECK (decision IN ('approved', 'changes_requested', 'approved_by_parent_cascade')),
    summary TEXT NOT NULL DEFAULT '',
    self_review INTEGER NOT NULL DEFAULT 0 CHECK (self_review IN (0, 1)),
    reviewed_by TEXT NOT NULL DEFAULT '' CHECK (length(reviewed_by) <= 120),
    basis_content_version INTEGER NOT NULL CHECK (basis_content_version >= 1),
    basis_handoff_id TEXT,
    operation_row_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    superseded_at TEXT,
    PRIMARY KEY (project_id, id),
    FOREIGN KEY (project_id, issue_id) REFERENCES issues(project_id, id),
    FOREIGN KEY (project_id, reviewer_session) REFERENCES sessions(project_id, id),
    FOREIGN KEY (project_id, requested_by_session) REFERENCES sessions(project_id, id),
    FOREIGN KEY (project_id, issue_id, basis_handoff_id) REFERENCES activities(project_id, issue_id, id),
    FOREIGN KEY (project_id, operation_row_id) REFERENCES operations(project_id, row_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX reviews_one_active ON issue_reviews(project_id, issue_id) WHERE superseded_at IS NULL;

CREATE TABLE issue_session_history (
    project_id TEXT NOT NULL,
    id TEXT NOT NULL,
    issue_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('created', 'started', 'unstarted', 'reviewed',
        'review_approved', 'review_changes_requested', 'closed')),
    operation_row_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (project_id, id),
    FOREIGN KEY (project_id, issue_id) REFERENCES issues(project_id, id),
    FOREIGN KEY (project_id, session_id) REFERENCES sessions(project_id, id),
    FOREIGN KEY (project_id, operation_row_id) REFERENCES operations(project_id, row_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX involvement_history ON issue_session_history(project_id, issue_id, session_id, action);

CREATE TABLE boards (
    project_id TEXT NOT NULL REFERENCES projects(id),
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    query TEXT NOT NULL,
    is_builtin INTEGER NOT NULL DEFAULT 0 CHECK (is_builtin IN (0, 1)),
    view_mode TEXT NOT NULL DEFAULT 'swimlanes' CHECK (view_mode IN ('swimlanes', 'backlog')),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision >= 1),
    last_viewed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT,
    PRIMARY KEY (project_id, id)
);

CREATE UNIQUE INDEX boards_live_name ON boards(project_id, name) WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX boards_one_builtin ON boards(project_id) WHERE is_builtin = 1 AND deleted_at IS NULL;

CREATE TABLE board_positions (
    project_id TEXT NOT NULL,
    board_id TEXT NOT NULL,
    issue_id TEXT NOT NULL,
    position INTEGER NOT NULL,
    added_at TEXT NOT NULL,
    PRIMARY KEY (project_id, board_id, issue_id),
    UNIQUE (project_id, board_id, position),
    FOREIGN KEY (project_id, board_id) REFERENCES boards(project_id, id),
    FOREIGN KEY (project_id, issue_id) REFERENCES issues(project_id, id)
);

CREATE TABLE session_focus (
    project_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    issue_id TEXT,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision >= 1),
    PRIMARY KEY (project_id, session_id),
    FOREIGN KEY (project_id, session_id) REFERENCES sessions(project_id, id),
    FOREIGN KEY (project_id, issue_id) REFERENCES issues(project_id, id)
);

CREATE TABLE notes (
    project_id TEXT NOT NULL REFERENCES projects(id),
    id TEXT NOT NULL,
    title TEXT NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    pinned INTEGER NOT NULL DEFAULT 0 CHECK (pinned IN (0, 1)),
    archived INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1)),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision >= 1),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT,
    PRIMARY KEY (project_id, id)
);

CREATE INDEX notes_live ON notes(project_id, archived, pinned, updated_at) WHERE deleted_at IS NULL;

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

CREATE INDEX activities_work_session ON activities(project_id, work_session_id, created_at, id)
WHERE work_session_id IS NOT NULL AND deleted_at IS NULL;

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

CREATE TABLE "operation_effects" (
    project_id TEXT NOT NULL,
    operation_row_id TEXT NOT NULL,
    entity_kind TEXT NOT NULL CHECK (entity_kind IN
        ('project', 'session', 'issue', 'board', 'note', 'focus', 'work_session', 'work_session_log')),
    entity_id TEXT NOT NULL,
    after_revision TEXT NOT NULL,
    state_version INTEGER NOT NULL CHECK (state_version >= 1),
    before_json TEXT CHECK (before_json IS NULL OR before_json IS JSON),
    after_json TEXT NOT NULL CHECK (after_json IS JSON),
    PRIMARY KEY (project_id, operation_row_id, entity_kind, entity_id),
    FOREIGN KEY (project_id, operation_row_id) REFERENCES operations(project_id, row_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX effects_entity ON operation_effects(project_id, entity_kind, entity_id);
