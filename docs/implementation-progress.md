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

### gh-7 — Issue mutation API

`POST /issues`, `PATCH /issues/{issue_id}`, `DELETE /issues/{issue_id}`, `POST /issues/{issue_id}/restore`를 project scope에 구현했습니다. typed command와 완전한 Issue DTO를 사용하며 빈 문자열·false·0·빈 labels 배열을 구분합니다. 생성 시 dependency와 creator involvement history를 동일 transaction에 저장하고, 수정은 If-Match를 검사하며 내용 변경 시 content version과 review supersession을 처리합니다. 부모 순환과 존재하지 않는 관계를 거부합니다. before/after issue와 dependency snapshot을 operation effect로 저장합니다.

검증: 실제 HTTP+SQLite에서 Unicode title 제한, enum/points/calendar date, unknown/case-mismatched/null 필드, 기본값, 428/412/409, canonical request retry와 byte-exact response/ETag, 필드 clear, soft delete/restore, parent cycle 및 실패한 dependency 생성 rollback을 확인했습니다. `go test -race ./...`, `go vet ./...` 통과.

남은 범위: list pagination/filter/token, 상세 context와 related record 조회, gh-7 전체 acceptance 확인. gh-8에서 기존 td와 substantive edit/review policy parity를 고정하고, gh-12에서 versioned effect를 사용하는 조건부 Undo를 구현합니다. 현재 runtime capabilities는 완성된 project/session 기능만 광고합니다.

### gh-7 — 완료: 목록·상세 조회와 acceptance

Issue 목록은 전체 필드, total/limit/offset/has_more와 project change token을 반환합니다. 기본 closed/deleted 제외, 상태·type·priority·labels·parent/epic·text 검색, 정렬과 ID tie-breaker, archive 조회를 지원합니다. records와 change counter는 같은 snapshot에서 읽습니다. 현재 조회는 project records를 메모리에서 필터링하며 TDQ와 monitor projection은 gh-11에서 확장합니다. search_mode=tdq는 현재 명시적 validation error이며 auto 검색은 text 동작입니다.

상세 조회는 logs/comments/latest handoff/children/dependencies/incoming dependencies/files 및 선택한 reviews를 같은 SQLite transaction에서 조회합니다. 배열은 항상 배열로 직렬화하며, soft-deleted Issue는 with_deleted=true로 조회하여 복원용 ETag를 얻습니다. 저장된 activity/file/review fixture와 project isolation, API의 filter/pagination/empty page 및 change token 변경을 검증했습니다.

gh-7 acceptance 확인: 실제 HTTP에서 missing If-Match 428, stale 412, unknown field 400, key fingerprint 충돌 409, 반복 mutation 응답/ETag 동일성을 확인했습니다. 동일 key 동시 8건에서 effect 1회, receipt 저장 실패와 deferred FK commit 실패의 전체 rollback은 실제 SQLite runner 통합 테스트로 확인했습니다. 수정 응답을 버린 뒤 서버·DB pool을 재시작하고 같은 요청의 status/body/ETag가 원래 receipt와 정확히 일치하고 revision이 한 번만 증가했음을 추가 검증했습니다. `go test -race ./...`, `go vet ./...`, OpenAPI validator 통과.

전체 구현 목표는 계속 진행 중입니다. Workflow/review parity, TDQ·Board·Note·WorkSession·Monitor·Undo·SSE, CLI/store/init 및 serve/GUI 연동은 별도 등록 task에 남아 있습니다.

## gh-8 — 진행 중: review 정책 일치

기존 td commit `c64d7873d8f555dd49875e34e6b39ae5257e2e0c`의 순수 reviewpolicy와 테스트를 domain 계층으로 이식했습니다. trusted/delegated/strict/balanced, minor, creator/implementer/history, attributed/self-review와 close-after-recorded-approval 판단을 기존 테스트 그대로 검증합니다. MIT attribution을 NOTICE에 추가했습니다.

Issue PATCH의 임시 보수적 review 무효화를 기존 정책의 필드 기준으로 교체했습니다. title/description/type/priority/minor/parent 변경은 content version을 증가시키고 승인을 supersede합니다. labels/points/date/sprint/acceptance 변경은 현재 td와 동일하게 approval을 유지합니다. 실제 SQLite 테스트로 metadata 변경에서도 aggregate revision은 증가하면서 content version과 review attribution은 유지되고, substantive 변경에서는 review와 attribution이 무효화되는지 확인했습니다.

검증: `go test -race ./...`, `go vet ./...`. gh-8은 진행 중이며 workflow/action API, review/handoff persistence와 cascade·관계 transaction 구현은 아직 남아 있습니다.

### gh-8 — workflow facts와 저장소

Application-owned WorkflowReader/WorkflowWriter ports를 추가하고 SQLite adapter에서 참여 이력, active review와 basis content version/handoff ID, typed activity/handoff/snapshot 저장·조회를 구현했습니다. application에 SQL handle을 노출하지 않습니다. Workflow facts loader는 같은 transaction의 Issue·Project policy·history·review·latest handoff로 reviewer/closer 판단 입력을 구성합니다.

유효한 approval은 decision, 미supersede 상태, 현재 content version, 최신 handoff ID와 해당 content version이 일치할 때만 인정합니다. 실제 SQLite에서 history 기반 구현 참여 판단, trusted acknowledgement와 delegated 거부, 오래된 basis 거부, handoff snapshot round trip, active review 유일성과 receipt FK 실패 시 history rollback을 검증했습니다. `go test -race ./...`, `go vet ./...` 통과. 상태 변경 service/HTTP, review/handoff orchestration, cascade와 relation commands는 계속 gh-8의 미완료 범위입니다.

### gh-8 — 기본 상태 전이와 handoff API

start/unstart/block/unblock/reopen/review와 handoff endpoint를 application workflow service에 연결했습니다. observed revision 확인 후 Issue·참여 history·활동 log·aggregate effect·receipt가 같은 transaction으로 저장됩니다. start는 implementer를 기록하고 unstart는 claim을 해제하되 참여 이력을 보존합니다. blocked 상태 start에는 force가 필요합니다. review에는 현재 content version의 handoff가 필요합니다.

Handoff는 review basis를 갱신하고 기존 승인을 supersede합니다. submit_review 기본 true일 때 in_progress Issue를 in_review로 함께 전환하며, false는 상태를 유지합니다. 모든 살아 있는 자식이 in_review/closed이면 부모를 재귀적으로 in_review로 변경하고 변경된 aggregate effect를 같은 transaction에 저장합니다.

검증: 실제 HTTP+SQLite에서 start/handoff byte-exact replay, start/unstart history, handoff snapshot·빈 배열, passive handoff 후 review, force/invalid snapshot을 확인했습니다. 부모 UPDATE에 실패를 주입하여 자식 변경·handoff·receipt rollback 후 동일 key 재시도가 성공하는 것을 검증했습니다. `go test -race ./...`, `go vet ./...` 통과. approve/close/reject, explicit status, review record-only/close-after-approval, 관계·활동 명령과 close cascade는 gh-8의 남은 범위입니다.

### gh-8 — 승인·종료·반려

approve/close/reject를 observed workflow service에 연결했습니다. trusted self-review와 attribution, delegated 독립 reviewer, record-only 승인, 기록된 승인에 기반한 다른 Session의 close를 지원합니다. 다른 reviewer의 승인을 종료할 때 reason을 요구하고 reviewer와 closed_by를 분리하여 보존합니다. force는 review 자격을 우회하지 않습니다. admin_reason은 owner만 사용할 수 있으며 security log를 남깁니다. 재오픈/반려는 기존 review를 supersede하고 승인 attribution을 제거합니다.

HTTP+SQLite에서 무기명 구현자 승인 거부, 잘못된 self-review/attribution 입력, delegated 우회 거부, record-only와 close attribution, 동일 승인 key 재생, 재오픈/반려 이력 보존을 검증했습니다. receipt INSERT 실패 시 review·closed state·참여 이력의 rollback과 동일 key 재시도를 검증했습니다. `go test -race ./...`, `go vet ./...` 통과.

남은 gh-8 범위에는 close/approval cascade, review 전용 endpoint와 explicit status, relation/activity commands 및 전체 parity/원자성 검증이 포함됩니다. self_close_exception의 비어 있지 않은 값은 아직 명시적으로 거부합니다.
