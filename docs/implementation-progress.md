# 구현 진행 기록

완료 여부와 의존 관계는 td의 GitHub-backed task 상태를 기준으로 합니다. 전체 목표는 등록된 backend·CLI·monitor/serve·GUI 연동 task 전부를 구현하고 검증하는 것입니다.

## gh-5 — Go 구성과 SQLite 저장소

Go module, composition root, 환경변수/flags 설정, health endpoint, graceful shutdown을 구현했습니다. domain model과 application-owned UnitOfWork/ReadStore ports는 HTTP/SQL adapter에 의존하지 않습니다. upstream domain 타입·validation helper의 MIT attribution을 보존했습니다.

SQLite adapter는 embedded 0001 migration과 checksum/미지원 version 검사, writer 1개와 reader 4개, WAL/FULL/connection별 foreign_keys·busy_timeout·query_only 설정, immediate write transaction, deferred read snapshot을 제공합니다. typed Project/Issue repository와 revision 조건부 save를 통해 후속 use case에서 SQL handle 없이 작업할 수 있습니다.

검증: `go test -race ./...`, `go vet ./...`. 실제 temporary DB/Go driver에서 project FK 격리, round trip, callback/commit 실패 rollback, scope 수명, scoped context의 nested write 거부, WAL snapshot, 동시 stale revision, migration 변경/unknown version과 listen 전 실패를 검증했습니다. 외부 API는 현재 health만 제공하며 미구현 endpoint를 capability로 광고하지 않습니다.
