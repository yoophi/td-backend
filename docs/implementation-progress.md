# 구현 진행 기록

완료 여부와 의존 관계는 td의 GitHub-backed task 상태를 기준으로 합니다. 전체 목표는 등록된 backend·CLI·monitor/serve·GUI 연동 task 전부를 구현하고 검증하는 것입니다.

## gh-5 — Go 구성과 SQLite 저장소

Go module, composition root, 환경변수/flags 설정, health endpoint, graceful shutdown을 구현했습니다. domain model과 application-owned UnitOfWork/ReadStore ports는 HTTP/SQL adapter에 의존하지 않습니다. upstream domain 타입·validation helper의 MIT attribution을 보존했습니다.

SQLite adapter는 embedded 0001 migration과 checksum/미지원 version 검사, writer 1개와 reader 4개, WAL/FULL/connection별 foreign_keys·busy_timeout·query_only 설정, immediate write transaction, deferred read snapshot을 제공합니다. typed Project/Issue repository와 revision 조건부 save를 통해 후속 use case에서 SQL handle 없이 작업할 수 있습니다.

검증: `go test -race ./...`, `go vet ./...`. 실제 temporary DB/Go driver에서 project FK 격리, round trip, callback/commit 실패 rollback, scope 수명, scoped context의 nested write 거부, WAL snapshot, 동시 stale revision, migration 변경/unknown version과 listen 전 실패를 검증했습니다. gh-5 시점의 외부 API는 health만 제공하며 미구현 endpoint를 capability로 광고하지 않았습니다.

Commit: `1f8ab52` (push 완료). td `gh-5`는 검증 결과를 log/handoff에 기록하고 명시적 self-review 후 closed 처리했습니다.

## gh-6 — Project ID·인증·Session API

RFC UUIDv7 Project ID와 UUIDv4 operation key 검증, CSPRNG 기반 ID generator, bootstrap token digest 저장/검증·변경 시 이전 token 폐기, membership 및 Session ownership 검사를 구현했습니다. reader도 자신의 Session을 등록할 수 있으며 writer 권한은 application authorization에서 별도로 검사합니다.

Project·owner membership·builtin Board·init response receipt를 같은 transaction에서 생성합니다. 동일 key의 동시 init 및 재시작 후 retry는 원래 status/body를 반환하고 다른 fingerprint에는 409를 반환합니다. Project PK 충돌만 최대 3번 새 후보로 시도하며 entropy/encoder 실패에는 전체 rollback합니다. Typed Project/Session commands를 HTTP에서 strict JSON으로 decode하며 duplicate/unknown/null 필드와 잘못된 ID/key를 거부합니다.

검증: 실제 HTTP server와 temporary SQLite를 사용한 `go test -race ./...`, `go vet ./...`. UUID bits/timestamp/CSPRNG failure, 동시 init 8건에서 effect 1회, byte-exact retry/restart, actor 권한·member revoke·Session ownership·reader write 금지, plaintext credential 미저장, token 교체, encoder/entropy rollback, bounded PK collision recovery를 확인했습니다. bootstrap token 누락은 DB 생성/listen 전에 거부합니다.

## gh-7 — 진행 중: command transaction 기반

공통 operation runner를 추가했습니다. membership/Session ownership을 retry에도 확인한 뒤 actor+project+key receipt를 조회하므로, 원래 요청의 revision이 이후 변경되었어도 성공 응답을 그대로 재생합니다. 신규 command의 aggregate 변경, versioned before/after effect, project change counter, encoded response receipt는 한 transaction으로 commit됩니다. transaction 실패 시 응답 bytes를 호출자에게 반환하지 않습니다.

실제 SQLite 검증: 동일 key 동시 요청 8건에서 callback/effect 1회, 재시작 후 원래 status/body/ETag 재생, fingerprint 충돌, 신규 key stale revision, membership 제거 후 replay 거부, encoder/receipt insert/deferred FK commit 실패 시 aggregate·counter·effect·receipt rollback. 강한 단일 If-Match parser와 누락 428 매핑도 추가했습니다. `go test -race ./...` 및 `go vet ./...`가 통과했습니다.

gh-7은 아직 완료되지 않았습니다. Issue CRUD application service·HTTP routes·전체 DTO와 endpoint acceptance 검증이 남아 있습니다.
