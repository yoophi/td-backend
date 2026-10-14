CREATE INDEX activities_work_session ON activities(project_id, work_session_id, created_at, id)
WHERE work_session_id IS NOT NULL AND deleted_at IS NULL;
