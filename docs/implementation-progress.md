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

### gh-8 — 부모 종료·의존 Issue 자동 해제

종료된 자식의 모든 sibling이 closed이면 epic 부모와 상위 epic을 재귀적으로 종료합니다. 각 부모에 closed attribution/history, activity log와 versioned effect를 기록합니다. 종료된 원래 Issue와 부모에 의존하는 blocked Issue는 모든 prerequisite이 closed인 경우만 open으로 바꾸고 implementer claim을 해제합니다. 응답에 모든 parent_status_updates와 auto_unblocked를 포함하며 중복 변경을 방지합니다.

기존 td를 확인하여 review/close 부모 cascade는 epic에만 적용하도록 정정했습니다. 삭제된 부모는 cascade 대상에서 제외하며, review 부모 cascade에도 activity log를 기록합니다.

검증: 실제 HTTP+SQLite에서 2단계 부모 종료와 dependent 해제, 미해결 prerequisite이 있는 dependent 유지, non-epic 부모 유지, 단일 operation의 aggregate effect 4개, byte-exact replay를 확인했습니다. dependent UPDATE 실패를 주입했을 때 자식·부모·상위 epic 종료가 모두 rollback되며 같은 key 재시도가 성공합니다. 전체 race suite 및 변경 후 cascade regression, `go vet ./...` 통과. gh-8의 나머지 관계·활동·review/status API와 epic 승인 시 descendant cascade 검증은 계속 진행합니다.

### gh-8 — 의존 관계와 파일 연결

POST/DELETE dependencies와 PUT files를 구현했습니다. 의존 관계 추가는 같은 project의 살아 있는 target을 확인하고 반복 탐색으로 self edge와 cycle을 거부합니다. 삭제는 project·source Issue·dep_id를 모두 확인합니다. 파일은 metadata만 저장하며 path/role 검증과 중복 거부, 빈 배열로 전체 clear를 지원합니다.

관계 변경은 revision/content version 증가·review supersession·관계 persistence·operation effect/receipt를 같은 transaction에서 처리합니다. Aggregate snapshot version 2는 files를 포함하며 이전/이후 연결을 보존합니다. 일반 Issue/workflow 변경도 파일 목록을 유지하는 snapshot을 기록하고, 전용 Files reader로 전체 Issue context를 불필요하게 읽지 않습니다. Undo 구현 gh-12는 기존 version 1과 version 2를 모두 해석해야 합니다.

검증: 실제 HTTP+SQLite에서 3-node cycle/self edge, 잘못된 source에서 삭제, project 간 연결 거부, file role/path/duplicate validation, 승인 무효화, 파일 INSERT 실패의 이전 연결/revision 보존과 같은 key 재시도, snapshot before/after files를 확인했습니다. `go test -race ./...`, `go vet ./...` 통과. gh-8의 활동·review/status endpoints와 나머지 cascade/parity 검증은 계속 진행합니다.

### gh-8 — explicit status와 record-only review API

POST status는 transaction 안에서 현재 상태를 확인해 기존 workflow action으로 해석하며 승인/종료 정책을 그대로 적용합니다. 동일 상태는 noop, 지원하지 않는 전이는 409입니다. review에서 in_progress로 복귀하면 기존 승인을 무효화하고 구현 참여 이력을 남깁니다.

POST reviews는 trusted/delegated에서 approved 또는 changes_requested를 기록하고 Issue를 in_review로 유지합니다. duplicate active approval, minor의 record-only 요청, strict/balanced 모드의 record-only를 거부합니다. changes_requested는 기존 승인을 supersede하고 승인 attribution을 제거합니다. self-review 기록은 security activity로 남기며 attribution의 newline/control character도 거부합니다. 기존 approve record_only 경로도 duplicate/minor/mode 규칙을 맞췄습니다.

HTTP+SQLite 검증: status+force 승인 우회 거부, independent reviewer의 status close, noop과 불가능한 전이, review status/body/ETag replay, duplicate 승인, changes_requested 이후 무승인 close 거부, strict/balanced mode gate 및 attribution 검증. `go test -race ./...`, `go vet ./...` 통과. 활동 API와 descendant approval cascade, 나머지 gh-8 parity 검증은 계속 진행 중입니다.

### gh-8 — 댓글·로그·공유 활동 API

POST comments/logs/activity, GET activity, DELETE comments를 구현했습니다. 모든 쓰기는 observed revision과 멱등 runner를 거치며 일반 댓글·로그는 content version과 승인을 유지합니다. passive handoff는 상태를 유지하고 review 기준을 갱신합니다. WorkSession 연결은 같은 project에서 소유 Session과 종료 여부를 확인합니다. activity 응답은 외부 operation key와 서버 attribution을 사용하고 git snapshot event도 보존합니다.

댓글 삭제는 soft delete로 audit history를 보존합니다. Activity 변경 command의 aggregate snapshot version 3은 전체 history 대신 변경된 활동의 before/after delta를 담습니다. gh-12 Undo는 version 1/2뿐 아니라 이 delta 형식도 처리해야 합니다. 일반 workflow 활동 이력은 감사 기록으로 유지합니다. request JSON의 nested null도 거부하도록 strict decoder를 보완했습니다.

HTTP+SQLite 검증: 댓글 replay, 댓글·로그의 승인 유지, soft-delete 이력 보존과 activity 조회 제외, 외부 UUIDv4 operation key, activity delta version, passive handoff의 비자동 review 제출과 승인 무효화, 다른/종료/없는 WorkSession 연결 거부, log type/message 및 nested null 검증. `go test -race ./...`, `go vet ./...` 통과. gh-8은 descendant approval cascade와 전체 policy/transaction acceptance 점검이 남아 있습니다.

### gh-8 — 완료: descendant 승인과 정책·원자성 검증

부모 승인 시 open/in_progress/in_review 자손을 `approved_by_parent_cascade` review와 함께 종료합니다. blocked/closed 자손은 상태를 유지하면서 그 아래 자손을 탐색합니다. 모든 descendant 변경·승인·history·effect·receipt는 한 transaction에 포함하고 `descendant_status_updates`로 반환합니다. 부모 승인 예외는 현재 content version에 결속하며 개별 handoff 없이도 유효합니다.

기존 td의 close 정책과 맞춰 issue 전체 구현 이력이 있으면 creator-open 우회를 거부합니다. 명시적 self_close_exception은 사유와 security log를 남기며, admin_reason과 함께 non-minor in_review의 승인을 우회하지 못합니다. balanced 생성자 승인은 다른 Session의 구현을 사유와 함께 승인할 수 있도록 standalone close 자격의 중복 검사를 제거했습니다. 기존 승인으로 종료하면서 새 reviewed_by를 전달하는 요청은 잘못된 attribution을 남기지 않도록 거부합니다. 직접 self-review 승인도 security activity를 기록합니다.

완료 기준 확인: 기존 policy 테스트와 실제 HTTP의 네 가지 mode/creator/minor/issue-wide history 검증이 통과했습니다. 기존 handoff/review basis 무효화, dependency cycle·project 격리, 부모 cascade rollback 테스트에 더해 descendant UPDATE 실패 시 부모 승인·자손 변경 전체 rollback, 같은 key 재시도 및 byte-exact receipt replay를 확인했습니다. `go test -race ./...`, `go vet ./...`, OpenAPI 구조/참조 및 GUI 필수 30 route 계약 검사, `git diff --check` 통과. 직접 코드와 upstream 승인·종료 경로를 대조한 self-review입니다.

이로써 gh-8의 workflow/review/handoff/관계·활동 범위를 완료했습니다. Board/TDQ/Note/WorkSession/monitor projection/Undo/SSE 및 CLI·monitor·serve 통합은 gh-9~19에서 계속 진행합니다.

## gh-9 — 진행 중: TDQ 문법과 Board 저장소

기존 td의 TDQ lexer/parser/AST와 해당 테스트를 domain/tdq로 이식했습니다. import를 backend domain으로 바꾸고 날짜 literal 검증 helper를 분리했습니다. SQL compiler나 DB 의존 실행기를 domain에 넣지 않았으며, 전체 query 평가와 batch facts는 후속 작업으로 남아 있습니다. upstream MIT attribution은 NOTICE에 기록했습니다.

Board에 revision/deleted_at을 추가하고 snapshot reader와 scoped writer ports를 구현했습니다. SQLite adapter는 project별 목록/상세·archive 조회, observed revision 저장, soft-delete 후 이름 재사용, 카드 위치 읽기와 원자적 교체를 지원합니다. 위치를 같은 writer transaction에서 삭제 후 삽입하므로 기존 카드가 sparse key를 교환할 때 중간 UNIQUE 충돌을 피합니다.

검증: 실제 SQLite에서 key 교환, stale revision, project 격리, 잘못된 다른 project의 Issue 연결과 중복 위치의 rollback, 카드 clear, archive/이름 재사용, callback 외부 scope 접근 거부를 확인했습니다. `go test -race ./...`, `go vet ./...`, `git diff --check` 통과. Board application/HTTP CRUD·builtin 보호, TDQ 후보 평가, slot/move 알고리즘과 endpoint 통합은 gh-9의 남은 범위입니다.

### gh-9 — Board 정의·설정·pin/unpin API

Board 목록/생성/수정/삭제/visit 및 명시적 position 추가·해제 HTTP endpoint를 application service와 연결했습니다. 모든 mutation은 소유 Session/project 권한, observed Board revision, 멱등 receipt와 같은 transaction의 aggregate effect를 사용합니다. Board effect version 1은 정의와 전체 위치 목록을 before/after로 보존하므로 gh-12 Undo의 입력으로 사용할 수 있습니다.

이름은 trim 후 빈 값을 거부하며 TDQ를 parse/validate합니다. 빈 query는 전체 선택을 뜻하고 생성 시 query 필드 자체는 필수입니다. builtin 보드의 name/query 변경과 삭제는 403이며 view mode·visit·position은 변경 가능합니다. DTO는 nullable last_viewed_at과 revision 문자열을 명시적으로 반환합니다. 삭제는 soft-delete이며 이후 이름 재사용이 가능합니다.

Position 입력은 기존 위치에서 이동 카드를 제외한 뒤의 1-based insertion slot입니다. 저장은 sparse key이고 간격 소진 및 정수 overflow/underflow 때 재배치합니다. 중간점 계산은 덧셈 overflow를 피합니다. 명시적 positions와 query 기반 visible order의 구분을 유지하며 before_id 이동은 후속 단계에서 구현합니다.

검증: HTTP+SQLite에서 builtin 제한, 유효하지 않은 TDQ/날짜/query 누락, 생성·visit·삭제·pin의 정확한 receipt replay, missing/stale revision, receipt 실패 시 Board revision/positions rollback 및 동일 key 복구, project 간 card/Board/Session 격리를 확인했습니다. versioned effect의 이전/이후 Board revision과 positions도 확인했습니다. 순서 테스트는 top/middle/end/reposition, gap exhaustion 및 정수 양 극단을 포함합니다. 전체 `go test -race ./...`, `go vet ./...`, OpenAPI/GUI 계약 검사와 `git diff --check` 통과. service 조립 정리 후 cmd/HTTP 테스트도 통과했습니다.

남은 gh-9 범위: TDQ 후보 평가와 Board detail 카드 projection, before_id move 및 해당 HTTP 회귀 테스트. capability는 아직 완성된 Board 기능으로 광고하지 않습니다.

### gh-9 — snapshot 기반 TDQ 평가기

기존 td의 순수 field matcher와 cross-entity AST 평가를 domain/tdq에 이식했습니다. SQL 변환과 DB 실행은 포함하지 않으며, typed Facts의 Issue·로그·댓글·handoff·파일·dependency·rework 데이터를 메모리에서 평가합니다. AND/OR/NOT 혼합 조건은 전체 AST를 평가하여 cross-entity 조건과 일반 필드의 논리 관계를 보존합니다. 스캔을 1만 건에서 자르지 않으며, nullable 정렬과 ID tie-breaker로 결정적인 결과를 반환합니다.

백엔드 보완: 기존 구현에서 누락되던 dep.blocks/dep.depends_on 및 활동 날짜 equality/inequality를 평가하고, Issue query에 note 전용 필드·prefix 단독 필드·지원하지 않는 함수 필드를 전달하면 오류로 반환합니다. 기존 문법에서 거부하던 cross-entity ordering 연산자는 계속 명시적으로 거부합니다. security activity log 타입을 query enum에도 포함합니다. Board 생성·수정 검증은 이 Issue query 검증 경로를 사용합니다.

검증: upstream evaluator 테스트와 추가 snapshot 테스트에서 혼합 논리, descendant/epic/dependency/file/Session 조건, 누락·삭제 prerequisite, 날짜의 일 단위·시각 단위 비교, nullable sort, 10,010개 입력의 후반부 일치 결과를 확인했습니다. 전체 `go test -race ./...`, `go vet ./...`, `git diff --check` 통과. 이름·주석 및 Board 검증 정리 후 TDQ와 HTTP 테스트도 통과했습니다.

아직 SQLite batch facts reader와 Board detail/move HTTP 연결은 남아 있습니다. gh-9을 완료로 표시하지 않았으며 gh-11의 일반 query/monitor 통합도 별도 작업입니다.

### gh-9 — 완료: snapshot 카드 조회와 before_id 이동

SQLite QueryReader는 Issue 수와 무관하게 5개 batch query로 Issue·활동·파일·의존 관계·rework facts를 읽습니다. Board 정의·TDQ 후보·명시적 위치는 같은 snapshot에서 읽으며, 이동은 같은 writer transaction에서 후보를 평가합니다. rework는 자유 형식 로그 대신 versioned operation effects의 review 전이를 사용합니다. gh-12 Undo 구현 시 취소된 전이의 rework 반영도 통합해야 합니다.

GET Board detail은 전체 Issue DTO와 sparse position을 반환합니다. before_id 이동은 필요한 visible prefix만 위치를 확정하며 숨겨진 카드의 key와 timestamp를 유지합니다. closed 카드 포함 여부, 간격 소진·overflow 재배치, 원자적 위치 교체를 지원합니다. category는 기존 td serve와 같은 빈 값이며 monitor 분류는 gh-11 범위입니다.

완료 기준을 직접 검토했습니다. 기존 builtin 보호·slot/sparse key·If-Match·project 격리 검증에 더해 실제 HTTP+SQLite에서 상세 query 결과, before_id 순서, closed/hidden 카드, byte-exact replay, stale revision, 위치 INSERT 실패의 전체 rollback과 동일 key 복구를 확인했습니다. WAL 동시 writer 상황에서도 facts가 기존 read snapshot을 유지하고 callback 밖 접근을 거부합니다. 작은 카드 집합의 모든 위치 부분집합·이동 조합과 hidden key 극단값을 검사했습니다. `go test -race ./...`, `go vet ./...`, OpenAPI/GUI 계약 검사와 `git diff --check`가 통과했습니다.

이로써 gh-9 범위를 완료했습니다. 일반 TDQ query/monitor API는 gh-11에서, Note·WorkSession은 gh-10에서 이어갑니다.

## gh-10 — Note lifecycle API

Note CRUD와 pin/archive를 scoped ports·SQLite adapter·application service·HTTP route에 연결했습니다. 생성 이후 변경은 If-Match와 operation receipt를 사용하며 Note revision·project change_seq·version 1 before/after effect를 같은 transaction에 저장합니다. 삭제는 soft delete이며 목록은 archived를 포함하고 deleted는 제외합니다. pinned와 갱신 시각으로 정렬하고 빈 content 및 false 해제를 보존합니다.

실제 HTTP+SQLite에서 생성/수정/삭제 byte-exact replay, missing/stale revision, pin 해제·archive·content clear, 다른 project/Session 거부, 삭제 이력과 escaped scope, receipt INSERT 실패의 전체 rollback 및 같은 key 재시도를 검증했습니다. project change_seq는 성공한 신규 operation마다 한 번 증가합니다. `go test -race ./...`, `go vet ./...`, OpenAPI/GUI 계약 검증이 통과했습니다. WorkSession과 Focus 내부 revision 통합은 gh-10의 남은 범위입니다.

### gh-10 — WorkSession start/read/tag/end

다중 Issue WorkSession의 scoped reader/writer와 HTTP API를 구현했습니다. 서버가 caller Session과 시작·종료 시각을 기록하고, 소유 Session만 태그 교체/종료를 수행할 수 있습니다. 종료 후 신규 변경은 거부하되 기존 operation key 재시도는 저장된 receipt를 그대로 반환합니다. local worktree 경로와 활성 WorkSession 선택은 서버에 저장하지 않습니다.

태그는 같은 project의 살아 있는 Issue를 모두 확인한 후 원자적으로 교체합니다. 빈 배열은 clear이며 null/누락/빈 ID/중복 ID는 거부합니다. 유지하는 태그는 tagged_at을 보존합니다. WorkSession revision과 태그를 version 1 aggregate effect로 저장해 조건부 Undo에 사용합니다. 내부 revision은 HTTP DTO에 노출하지 않는 기존 계약을 유지합니다.

실제 HTTP+SQLite 테스트에서 다른 Session 수정 거부·공유 조회, 다른 project Issue 혼합 요청의 전체 거부, receipt 실패 rollback과 같은 key 복구, 시작/태그/종료 replay, 종료 후 변경 거부, 빈 태그 및 change_seq를 검증했습니다. WorkSession revision과 전체 tag timestamps의 before/after snapshot, callback 밖 reader 거부도 확인했습니다. Focus 내부 revision과 session focus API는 gh-10/gh-11 연계 범위로 남아 있습니다.

WorkSession 변경 후 전체 `go test -race ./...`, `go vet ./...`, OpenAPI/GUI 계약 검사, `git diff --check`가 통과했습니다.

### gh-10 — 완료: Session Focus와 내부 Undo revision

GET/PUT Focus를 구현했습니다. project와 caller Session을 key로 사용하고, 같은 project의 살아 있는 Issue만 선택합니다. `issue_id: null`은 해제이며, 해제 후에도 row와 증가된 revision을 보존합니다. 미설정 focus의 revision 0과 저장된 clear 상태를 구분하고 version 1 before/after effect에 기록합니다. WorkSession과 같이 focus revision은 내부 Undo 기준이며 HTTP에는 노출하지 않습니다.

strict JSON decoder에 명시적인 최상위 nullable 필드 허용 경로를 추가했습니다. Focus의 issue_id만 null을 허용하며 필드 누락·duplicate·대소문자 변형·잘못된 타입·중첩 null은 거부합니다. 다른 endpoint의 null 거부 규칙은 유지합니다.

실제 HTTP+SQLite에서 서로 다른 Session의 독립 focus, 다른 project/삭제 Issue 거부, clear 및 초기 clear, revision·change_seq, byte-exact replay, clear 이후 과거 set 재시도의 무변경, receipt 실패의 전체 rollback 및 동일 key 복구를 검증했습니다. Versioned effect가 이전 선택과 clear revision을 보존하고 callback 밖 reader 접근을 거부하는 것도 확인했습니다.

gh-10 완료 기준을 직접 검토했습니다. Note soft deletion/revision, WorkSession 소유권·종료 후 수정 제한·태그 clear/project 격리, Focus/WorkSession 내부 revision 및 change counter를 실제 HTTP/SQLite 테스트로 확인했습니다. 일반 query/monitor projection 및 Focus를 포함한 projection 통합은 gh-11, 저장된 effect의 조건부 Undo 실행은 gh-12에서 이어갑니다.

Focus 통합 후 전체 `go test -race ./...`, `go vet ./...`, OpenAPI/GUI 계약 검사 및 `git diff --check` 통과. 직접 구현과 완료 기준을 대조한 self-review입니다.

## gh-11 — 일반 TDQ query endpoint

GET /query?q=...를 Issue application service에 연결했습니다. 동일 read snapshot에서 project 권한과 Session을 확인하고 batch facts를 읽어 TDQ를 평가합니다. caller Session으로 @me를 해석하고, query가 선택한 closed Issue도 반환하되 soft-deleted Issue는 제외합니다. 결과는 TDQ sort와 ID tie-breaker로 정렬하며 제한 없는 ID 배열입니다. 빈 q는 허용하지만 q 누락/중복/추가 query parameter는 거부합니다.

실제 HTTP 테스트에서 정렬, implementer=@me의 Session별 결과, closed 선택과 deleted 제외, empty q, 잘못된 TDQ/note predicate와 잘못된 query parameters의 400을 확인했습니다. 일반 Issue 목록의 search_mode TDQ 연결 및 monitor projection은 아직 남아 있습니다.

일반 query endpoint 추가 후 전체 `go test -race ./...`, `go vet ./...`, `git diff --check` 통과.

### gh-11 — Issue 목록의 TDQ 검색과 snapshot pagination

Issue 목록에 auto/text/tdq 검색을 연결했습니다. auto는 유효한 TDQ를 사용하고 parse/field validation 실패 때만 텍스트 검색으로 fallback합니다. 명시적 tdq는 오류를 반환하며 text는 ID/title/description/acceptance를 검색합니다. TDQ 결과와 status/type/priority/labels/parent/epic 필터를 교차 적용한 뒤 pagination합니다. 명시적인 sort/order가 있으면 해당 정렬을, 없으면 TDQ sort를 유지하며 ID tie-breaker를 사용합니다.

QueryReader에 with_deleted 선택을 추가하여 archive 조회에서도 삭제된 Issue의 활동/파일/관계 facts를 같은 snapshot에서 읽습니다. 일반 query와 Board는 계속 삭제된 Issue를 제외합니다. TDQ 검색은 Issue별 쿼리 없이 기존 5개 batch query를 사용합니다.

HTTP 검증: auto fallback/forced text/invalid TDQ, TDQ sort와 명시적 정렬 우선순위, 기존 필터와 교집합, full DTO, 페이지 total/token, closed 기본 제외와 명시적 선택, with_deleted와 이전 댓글 조건을 확인했습니다. 별도 SQLite writer가 project token 조회 이후 Issue와 change_seq를 변경하도록 주입했을 때 records/total/token은 모두 이전 snapshot을 유지하고 다음 요청만 새 snapshot을 보는 것을 검증했습니다. monitor projection과 available_transitions/presence 연계는 gh-11의 남은 범위입니다.

목록 검색 변경 후 검증: `go test -race ./...`, `go vet ./...`, OpenAPI/GUI 계약 검사와 `git diff --check` 통과했습니다.

### gh-11 — Monitor용 batch policy facts

WorkflowReader.Facts를 추가하여 live Issue의 참여 이력, active review basis, 최신 handoff를 세 개의 project-scoped query로 읽습니다. 최신 handoff는 Issue별 window rank로 선택하며 review freshness는 기존 application policy에 맡깁니다. 기존 snapshot/transaction과 scope 수명 제한을 그대로 사용합니다.

실제 workflow로 생성한 이력·handoff·record-only 승인에 대해 batch 결과와 기존 단건 reader 결과가 같은지 확인했습니다. 내용 수정에 따른 승인 무효화, soft-delete 후 facts 제외, 다른 project 격리와 callback 밖 접근 거부도 검증했습니다. 이 reader를 monitor 분류와 available_transitions에 연결하는 작업은 계속 진행합니다.

Batch policy facts 추가 후 전체 `go test -race ./...`, `go vet ./...`, `git diff --check` 통과.

### gh-11 — Monitor snapshot HTTP projection과 presence

Monitor service/HTTP/DTO를 연결했습니다. 같은 read snapshot에서 project/token, TDQ facts, batch policy facts, Board 위치, Focus, 진행 중 Issue, 활동 feed, 최근 handoff 및 Session presence를 읽습니다. Issue별 추가 query는 없으며 모든 카드에 full DTO와 권한별 available_transitions를 제공합니다. 검색은 작업 목록에 적용하고 Focus와 전체 in-progress 목록은 유지합니다. Board query와 검색의 교집합을 기존 sparse 위치 순서로 분류합니다. 명시적 sort는 TDQ sort를 덮어쓰며 문자열을 연결하지 않고 검증된 AST sort를 교체합니다.

승인 분류는 기존 reviewpolicy와 content/handoff basis를 사용합니다. trusted 구현자의 self-review 경로와 delegated의 pending-review, strict/balanced 분류를 유지합니다. 활성 approval을 가진 Issue에는 ready_to_close와 review summary를 제공하며 reader role에는 명시적인 빈 전이 배열을 반환합니다. 활동 feed는 최신 50개이고 최근 24시간 handoff는 별도 조회하여 feed cap과 관계없이 반환합니다.

새 operation은 같은 writer transaction에서 소유 Session의 last_activity도 갱신합니다. POST sessions/{session_id}/heartbeat는 빈 body와 operation key를 사용하며 caller Session만 허용합니다. 새 heartbeat는 change_seq를 증가시키고 replay는 원래 timestamp/token을 유지합니다. Monitor presence는 최근 5분 Session과 현재 요청 Session을 표시합니다. CLI/serve heartbeat 호출은 이후 통합 작업에 포함됩니다.

실제 HTTP+SQLite 검증: 네 가지 정책별 review 분류/approve 가능 여부, record-only approval 후 close, closed 포함 옵션, 의존 관계/Focus/full fields, reader 전이 배열, 검색·TDQ 오류·sort 검증, rework 후 review reset, Board 이동 순서와 검색 교집합, 활동 50개 제한과 동일 Issue의 다중 handoff를 확인했습니다. 별도 writer가 token 조회 후 Issue/Focus/change_seq를 변경해도 응답이 이전 snapshot을 유지합니다. Presence expiry·heartbeat replay/소유권·작업 쓰기 갱신과 last_activity UPDATE 실패의 전체 rollback/같은 key 복구도 검증했습니다.

gh-11은 stats·labels·프로젝트 활동 조회 및 전체 조회 계약의 최종 점검이 남아 있습니다. 완료로 표시하지 않았습니다.

Monitor/presence 통합 후 전체 `go test -race ./...`, `go vet ./...`, OpenAPI/GUI 계약 검사 및 `git diff --check` 통과.

### gh-11 — Stats·labels 및 조회 범위 완료 점검

Stats와 labels HTTP API를 추가했습니다. 살아 있는 Issue의 전체 필드로 total/status/type/priority/points/completion과 oldest-open/newest/last-closed를 계산합니다. 날짜 집계는 backend UTC의 오늘과 현재 시각부터 최근 7일입니다. 동률인 시각은 ID 오름차순으로 결정합니다. 활동 수와 most-active Session은 별도 한 번의 batch 집계이며, 기존 td와 같이 삭제된 Issue에 보존된 활동도 포함하고 삭제된 활동은 제외합니다. labels는 살아 있는 Issue에서 중복을 제거하여 정렬하며 workflows=[]/default_workflow=standard를 유지합니다.

빈 프로젝트의 nullable highlights/0 집계, 날짜 경계, 삭제 제외, full fields, points 평균/completion, log/handoff count, labels 중복 및 archive 제외, project/Session 격리를 실제 HTTP+SQLite로 검증했습니다. 두 stats read 사이 별도 writer의 Issue 삭제와 log 추가를 주입했을 때 이전 Issue 수와 이전 activity 수가 일치하고 다음 요청만 새 snapshot을 보는 것을 확인했습니다. 전체 race/vet/OpenAPI 검증이 통과했습니다.

gh-11 description과 API 설계를 재점검했습니다. activity 범위는 이미 구현된 Issue activity 조회와 Monitor의 project feed입니다. 별도 프로젝트 activity endpoint는 명세에 없으므로 추가할 필요가 없습니다. 앞선 진행 기록에서 project activity를 남은 별도 endpoint로 표현한 부분을 이 점검으로 정정합니다. list/detail/board/monitor/activity/stats/labels/query 경로와 full DTO/total/token snapshot, batch policy facts, 분류/전이, Focus/presence 및 TDQ 오류 검증이 모두 구현돼 있습니다. 완료한 runtime capabilities를 공개하며 Undo/SSE/changes는 아직 광고하지 않습니다.

직접 완료 기준과 구현을 대조한 self-review입니다. 조건부 Undo·변경 이벤트와 CLI/monitor/serve/GUI 연결은 gh-12~19에 남아 있어 전체 구현 목표는 계속 진행합니다.

## gh-12 — Operation audit 조회와 effect reader

Operation 상세/list API를 application/SQLite/HTTP에 연결했습니다. 저장된 receipt의 응답 status/body와 Session/method/path/시각을 반환하며, 내부 row ID·Actor ID·fingerprint는 노출하지 않습니다. 상세 조회는 project와 외부 UUIDv4 operation key로 검색합니다. actor-scoped idempotency 특성상 다른 Actor가 같은 key를 쓴 경우 409로 거부하여 임의의 receipt를 선택하지 않습니다. 목록은 Session/Issue effect 필터와 안정적인 pagination을 지원합니다.

조건부 Undo가 사용할 project-scoped effect reader도 추가하여 versioned before/after와 after-revision을 읽습니다. 기존 actor/scope 기반 byte-exact replay lookup은 그대로 유지합니다. 실제 HTTP+SQLite에서 저장 응답, filter/page order, 생성 effect version/nullable before, project 격리, scope 수명, malformed/missing key, Actor key 모호성 거부와 기존 actor replay 유지를 검증했습니다. 전체 race/vet/OpenAPI 검사와 추가 collision regression의 race 검증을 수행했습니다.

gh-12 남은 범위는 모든 affected aggregate의 after-revision 검사, Issue/Board/Note/Focus/WorkSession 원자적 Undo, 승인/history 보존과 지원하지 않는 operation 거부입니다. Undo capability는 아직 공개하지 않습니다.

### gh-12 — 공유 aggregate 조건부 Undo와 migration

Undo command runner와 HTTP endpoint를 연결하고 Board/Note/Focus/WorkSession inverse를 구현했습니다. 모든 effect의 현재 after-revision을 먼저 확인하며 하나라도 후속 변경이 있으면 409입니다. inverse·새 revision·effect·취소 사유/원본 연결·Session activity·change_seq·receipt는 같은 transaction에 저장합니다. 기존 Undo의 재-Undo와 Project/Session 생성 및 heartbeat Undo는 거부합니다. Focus/WorkSession은 소유 Session만 취소합니다.

Board는 전체 sparse positions와 정의를 복원하고 Note 삭제는 이전 live 상태를 복원합니다. 생성 취소는 soft-delete이며 Focus의 생성 취소는 증가한 revision을 가진 clear row로 표현합니다. WorkSession 생성 취소를 위해 immutable 0001을 유지하고 0002 migration에 deleted_at과 operation_undos audit 연결을 추가했습니다. 삭제된 WorkSession은 일반 조회·활동 연결에서 제외하고 Undo용 reader만 보존 상태를 읽습니다.

실제 HTTP/SQLite 검증: 필드 수정·삭제·생성·pin·Focus·tag/end inverse, 증가하는 revision, byte-exact replay와 원본 재취소 거부, 다른 Session의 개인 aggregate 거부, 사유 audit, mixed effect 중간 쓰기 실패의 전체 rollback 및 같은 key 복구, 뒤쪽 effect 후속 변경의 앞쪽 aggregate 무변경을 확인했습니다. 이전 state의 omitted nullable 값이 복원 대상에 남지 않도록 zero-state decode를 사용합니다. 실제 version 1 DB를 version 2로 올려 기존 WorkSession의 보존과 archive/activity 접근 제한을 검증했습니다. 전체 race/vet/OpenAPI 및 추가 Board restore/create 회귀 race 검증이 통과했습니다.

현재 Issue effect version 1/2/3은 준비 단계에서 409로 거부합니다. Issue 필드·관계·파일·activity delta inverse와 다중 Issue cascade/approval/history/TDQ rework 연계는 gh-12의 남은 범위입니다. 전체 Undo capability는 아직 광고하지 않습니다.

### gh-12 — 완료: Issue·관계·활동 Undo와 cascade

Issue effect version 1/2/3을 지원합니다. revision/content_version을 증가시키며 업무 필드와 dependency/file state를 복원합니다. version 1에서 기록하지 않은 파일은 유지합니다. creation inverse는 soft delete이며 삭제 inverse는 live 상태를 복원합니다. 활동 delta는 기존 row의 soft-delete 상태를 복구하거나 새 활동을 숨기고 본문/attribution/audit row는 보존합니다. Undo는 security log와 version 3 inverse effect를 기록합니다.

모든 Issue inverse는 기존 승인을 supersede하고 reviewer attribution을 제거합니다. 기존 participation/review rows를 삭제하거나 이전 handoff basis를 다시 유효하게 만들지 않습니다. 취소된 workflow operation은 TDQ rework 계산에서 제외하므로 review 제출의 inverse 후 원래 rework를 다시 표시합니다. 복원 결과에서 부모/의존 관계 그래프의 순환을 확인하고 발생 시 전체 inverse를 rollback합니다.

실제 HTTP+SQLite에서 필드·파일·legacy v1·생성·삭제 inverse, 증가하는 revision/content_version, activity append/delete delta, 승인 supersession/audit 보존, 이후 변화의 409, dependency/parent cycle rollback, 새로운 review Undo 이후 rework 복원을 검증했습니다. 실제 epic 승인 cascade를 취소하면서 모든 자손/부모 상태와 revision을 함께 복원하며, 중간 Issue UPDATE 실패의 전체 rollback 및 같은 key 복구를 확인했습니다. 참여 history가 그대로 유지되고 review rows는 보존된 채 superseded되며 예전 승인을 사용한 close는 거부됩니다. unknown effect version과 Project/Session creation 및 Undo의 재-Undo도 거부합니다.

gh-12 완료 기준을 직접 대조한 self-review입니다. 공유 aggregate Undo 테스트와 합쳐 모든 affected aggregate의 after-revision 검사, 다중 effect 원자성, 증가하는 revision, approval/history 보존을 검증했습니다. 전체 race/vet/OpenAPI와 추가 unsupported-version/capability 회귀를 통과했습니다. operations/undo capabilities를 공개합니다. Project changes/SSE와 CLI/monitor/serve/GUI 연결은 gh-13~19에서 계속 진행합니다.

## gh-13 — Project changes와 SSE hub

GET changes와 events를 구현했습니다. project hub는 subscriber가 있을 때만 250ms마다 committed change_seq를 짧은 read snapshot으로 읽습니다. 같은 project의 subscriber는 poller 하나를 공유하며 capacity-one queue가 오래된 invalidation을 최신 token으로 교체합니다. 마지막 subscriber가 해제되면 poller를 취소하며 전체 subscriber는 기본 128개로 제한합니다. SSE 대기/전송 중에는 DB scope/connection을 유지하지 않습니다.

초기/reconnect cursor가 현재 token과 다르면 refresh를 전송하며 같으면 다음 ping/change를 기다립니다. id는 opaque change_token이고 refresh payload는 token/timestamp, ping은 token만 포함합니다. 기본 ping은 15초입니다. 각 write/flush에 5초 deadline을 적용하며 context cancellation, hub 종료, 권한·credential 철회 또는 read/write 실패 시 구독을 해제합니다. event/ping 전 credential과 project/Session 권한을 다시 확인합니다. 긴 서버 WriteTimeout 대신 SSE write별 deadline을 사용하고 hub 수명은 서버 context에 연결했습니다.

실제 HTTP+SQLite 테스트에서 payload/ID, 새 mutation refresh, receipt replay의 token 유지, 이전/동일 cursor 재접속, ping, query/header 오류, membership/token revocation 후 stream 종료를 확인했습니다. 느린 subscriber가 최신 notice만 받고 빠른 subscriber를 막지 않는 것과 subscriber cap/shutdown도 검증했습니다. reader pool보다 많은 8개 stream을 열어도 조회가 가능하며 hub 종료는 HTTP stream을 닫습니다. 쓰기를 받지 못하는 transport에 deadline을 주입한 테스트에서 handler 종료와 subscriber 해제를 확인했습니다. 전체 race/vet/OpenAPI 및 추가 deadline regression을 통과했습니다.

gh-13 완료 기준을 직접 대조한 self-review입니다. changes/events capabilities를 공개합니다. 서버 이벤트의 CLI polling/td serve relay와 GUI 소비는 gh-14~19 통합 범위로 계속 진행합니다.

## gh-14 — CLI init 실제 서버 검증 (진행 중)

CLI integration worktree에서 backend store parser/config/init과 durable idempotency intent를 구현 중입니다. 초기화의 유실 응답·로컬 config 저장 실패·동시 실행·충돌 연결 거절은 targeted race 테스트를 통과했습니다. `scripts/check_cli_init.py`는 빌드한 서버/CLI 바이너리를 받아 임시 SQLite 서버와 Git 저장소에서 최초 생성, 반복 init, worktree의 동일 Project 재사용, 명시 Project attach, 충돌/잘못된 ID 거절 및 로컬 SQLite/token 미저장을 검증합니다. 실제 두 바이너리로 통과했습니다.

실행 예: `python3 scripts/check_cli_init.py --backend /absolute/path/td-backend --cli /absolute/path/td`.
CLI의 전체 release-safe `make test`는 진행 중이며 gh-14는 아직 완료 처리하지 않았습니다. issue reader/workflow, monitor, serve/GUI 연동은 후속 gh-15~19 범위입니다.

## CLI store 분기와 HTTP transport 기반

CLI integration branch의 `d37f15c`는 기존 GitHub wrapper가 SQLite 이외 store를 전부 GitHub로 처리하던 분기를 수정합니다. GitHub는 명시적으로 선택했을 때만 진입하며, 아직 연결 전인 backend command는 기존 backend guard에서 오류를 반환합니다. backend list/show/query/search/start/close guard(race), 기존 SQLite check-handoff와 GitHub routing/offline query 회귀를 통과했습니다.

`603b745`는 후속 gh-15/16의 HTTP 기반을 추가합니다. Issue DTO는 CLI model과 observed revision을 함께 유지합니다. List는 offset 페이지를 모두 수집하고 opaque change token이 달라지면 처음부터 재시작합니다(최대 3회). 페이지 metadata·중복 ID·잘못된 revision·진전 없는 페이지는 부분 성공 대신 오류로 반환합니다. 쓰기는 caller의 UUIDv4 key와 관찰한 revision의 strong If-Match를 보내며 자동 재시도하지 않습니다. status별 typed APIError와 keyed write의 응답 유실/손상 및 5xx 결과 불확실 오류를 구분합니다. 서버 오류 body/credential은 출력하지 않으며 redirect credential 전송을 차단합니다.

pagination restart/불안정 snapshot/중복·빈 페이지·nullable DTO/cancel, If-Match/key 전달, stale·auth·conflict·store 오류, 불확실 write의 key 보존, redirect 회귀가 race 검사와 vet를 통과했습니다. caller Session의 장기 연결 및 OpenReader/CLI command dispatch는 아직 후속 작업입니다. 전체 CLI make test는 계속 실행 중이므로 gh-14/15를 완료로 처리하지 않았습니다.

## gh-15 기반 — durable Caller Session과 원격 Reader

CLI integration branch `575b314`에서 backendstore.Open과 원격 Reader를 연결했습니다. 공유 Project association과 별개로 device/checkout/branch/agent/명시 context/credential digest별 등록 intent를 사용자 config 디렉터리에 저장합니다. 등록 UUIDv4 key와 immutable command를 HTTP 전에 원자 기록하고, 응답 유실이나 session-ID 저장 실패 시 같은 key/payload로 복구합니다. credential 변경 시 다른 Session context를 쓰며 token 원문은 저장하지 않습니다. 파일은 0600이고 HTTP 중 config lock을 유지하지 않습니다. Project 접근은 등록된 Session으로 다시 확인합니다.

OpenReader의 backend 경로는 typed client로 조회하며, 실제 checkout을 명시하는 OpenReaderWithContext도 제공합니다. Reader Record는 observed revision을 보존합니다. 로컬 SQLite와 GitHub client는 사용하지 않습니다. 유실 등록 복구/동시 등록 중복 방지/명시 context·credential·checkout 분리/permissions/token 미저장 및 Reader의 Session header/revision/cancel/no-DB 회귀가 race와 vet를 통과했습니다. CLI list/show/query/search/stats 직접 dispatch는 다음 작업이며 gh-15 전체를 완료 처리하지 않았습니다. 기존 전체 make test(60m)는 아직 실행 중입니다.

## gh-15 진행 — 원격 show와 실제 CLI 조회 검증

CLI integration branch `1ae3800`에서 show/context/view/get의 backend 분기를 연결했습니다. 서버 detail의 Issue/revision/review history/log/handoff/comment/file/dependency/children을 읽고 JSON/short/long/Markdown, 단일·복수 ID와 unique prefix, Session focus 또는 단일 활성 Issue 선택을 지원합니다. tree는 일관된 전체 목록에서 구성하고 cycle·없는 root를 거절합니다. 여러 detail과 human 문자열을 출력 전에 준비하여 중간 실패를 성공처럼 출력하지 않습니다.

상세 JSON·prefix·focus·children/tree·복수 Issue·short 출력, 잘못된 format의 무요청 거절과 복수 조회 중 실패 시 무출력 회귀가 race/vet를 통과했습니다. 실제 서버/SQLite에 epic/child를 생성한 뒤 CLI show/prefix/children/tree/multiple JSON을 검증하도록 `scripts/check_cli_init.py`를 확장했고 통과했습니다. 임시 Caller context도 격리합니다. list/query/search/stats와 workflow/monitor/serve는 계속 진행합니다.

기존 전체 make test는 모든 non-cmd 패키지가 통과했지만 cmd에서 새 --project-id에 따른 inventory mismatch와 누적 실행 60분 timeout으로 종료했습니다. timeout 당시 활성 테스트는 TestGitHubTransitionRoutingAndFocus(12초)였으며 단일 테스트가 60분 멈춘 증거는 아닙니다. inventory를 갱신한 뒤 전체 gate를 다시 확인할 예정이며 아직 gh-14를 완료하지 않았습니다.

## gh-15 진행 — 원격 TDQ query

CLI query의 backend 분기를 기존 TDQ 평가기에 연결했습니다. 전체 Issue 페이지를 읽고 필요한 detail(log/comment/handoff/file/dependency)은 request-local snapshot에서 lazy 조회하며, rework/open-dependency 집합은 서버 TDQ로 확인합니다. Project changes token을 계산 전후 비교하여 중간 변경 시 전체 계산과 lazy cache를 폐기하고 최대 3회 재시작합니다. 변경 중 delete로 생긴 일시적 404도 새 token이면 재시도하며, 안정된 상태의 오류는 그대로 실패합니다. 결과는 최종 안정 확인 후에만 출력합니다.

기존 predicate/현재 Session/cross-entity와 sort/limit/max-scan 및 table/JSON/ids/count를 유지합니다. malformed query는 네트워크 전에 거절합니다. regular/comment/rework/Session predicate, scan cap 경고, token 변경·일시 오류 전체 재시작, 안정 오류 보존과 bounded retry 회귀가 race와 vet를 통과했습니다. 실제 서버/SQLite smoke에서 type, descendant_of 및 comment.text 조회도 통과했습니다. 댓글 생성 smoke는 API 계약대로 observed revision의 If-Match를 전달합니다.

CLI inventory는 init --project-id 한 항목만 각 sync snapshot에 추가하여 f74ae2c 커밋·push했고 isolated inventory/GitHub transition 검증을 통과했습니다. 전체 release-safe make test는 TEST_TIMEOUT=120m으로 재실행 중입니다. list/search/stats 및 workflow/monitor/serve 연결은 계속 진행합니다.

## gh-15 진행 — 원격 ranked search

CLI search를 backend snapshot에 연결했습니다. GitHub와 backend의 순위 계산은 공통 함수로 추출하여 기존 SQLite LIKE wildcards와 filter/score/tie ordering을 유지합니다. backend의 lazy detail은 log와 최신 handoff를 검색하며 comment는 검색 대상에서 제외합니다. 모든 조회/순위 계산은 전후 change-token 확인을 통과한 뒤 출력합니다. status/type/priority/labels, limit과 show-score/JSON을 지원합니다.

기존 GitHub search 및 원격 own-field/log 순위, status filter, score 출력과 comments 제외 회귀가 race/vet를 통과했습니다. 실제 서버/SQLite에 observed revision으로 log를 추가하고 log-only ranked search 및 comment 제외 smoke를 통과했습니다. CLI 구현은 e3f2cc0에 커밋했습니다.

두 번째 전체 make test(120m)는 약 57분에 완료됐고 non-cmd 전체 및 cmd의 다른 테스트는 통과했습니다. 유일한 실패인 GitHub support matrix의 새 project-id 분류를 unsupported로 기록하여 3d842b5에 커밋·push했고 inventory/matrix isolated 검증이 통과했습니다. 초기화 gh-14 review/approve 상태 전환을 진행 중이며 실제 상태 확인 전 완료로 공지하지 않습니다. 최종 최신 CLI 전체 통합 gate는 gh-19에서 확인합니다.

## gh-14 완료 및 gh-15 목록·통계 연결

gh-14는 실제 acceptance를 대조한 self-review로 승인·종료했고 gh-15를 시작했습니다. 초기화의 parser/UUID/access/conflict/durable key/recovery/atomic config/worktree evidence와 commit/push를 handoff에 기록했습니다. 최종 최신 CLI 전체 통합 gate는 gh-19에서 확인합니다.

원격 list의 일반·TDQ 필터, status/all/open/type/id prefix/labels/priority/search/points/date/implementer/reviewer/mine, parent/epic 및 dot context, scheduling, sort/reverse/limit, JSON/long/short를 연결했습니다. dot context는 focus, Session active work·log/handoff의 공통 root, active WorkSession의 tagged Issue를 순서대로 확인하며 cycle/ambiguity를 거절합니다. reviewable/ready-to-close는 서버 monitor의 정책 평가 결과로 분류합니다. lazy 상세 출력과 모든 필터 처리는 전후 token 확인을 통과한 후에만 출력합니다.

프로젝트 통계 명령인 info도 remote stats 및 review buckets를 일관된 compound read로 연결했습니다. 로컬 DB를 표시하지 않고 store/URL/Project/Session을 표시합니다. total/grouping 필드가 없는 성공 응답을 0건 통계로 표시하지 않고 거절합니다. 목록 필터·hierarchy/TDQ·상세 로그·review 분류와 통계 JSON 회귀가 race/vet를 통과했습니다. 실제 backend SQLite smoke에서도 type/parent/epic/comment TDQ/sort/limit 목록과 info의 total/type/Project 연결을 확인했습니다.

CLI workflow/history/Undo 및 monitor/serve 전체 연결은 gh-16~19에서 계속 진행합니다.

## gh-15 완료 및 gh-16 durable write intent 기반

gh-15는 실제 acceptance와 Reader/Client/CLI 읽기 경로를 대조한 self-review로 승인·종료했습니다. 패키지 race/vet 및 실제 backend SQLite CLI smoke의 list/show/query/search/info 검증과 각 commit/push를 handoff에 기록했습니다. gh-16의 쓰기/Workflow/History/Undo 연결을 시작합니다.

backendstore.RunIntent는 semantic invocation의 hash별 요청을 사용자 config의 Session-scoped operations 디렉터리에 저장합니다. HTTP 전에 UUIDv4 key/method/resource/observed revision/JSON body를 0600 파일에 원자 기록합니다. pending 요청은 준비 함수를 다시 호출하지 않고 기존 payload/revision/key로 receipt를 복구합니다. verified success 또는 definite 4xx 이후 새 실행은 fresh request를 준비하고, 완료 전에 시작된 concurrent 실행은 원래 receipt를 사용합니다. network/decode/response verification 또는 완료 기록 실패는 uncertain으로 남겨 성공으로 보고하지 않습니다. 파일 lock 동안 HTTP나 prepare를 실행하지 않습니다.

응답 유실·immutable read-modify-write 복구, 검증 실패, 로컬 완료 저장 실패, stale의 무재시도/새 실행 reobserve, concurrent prepare 이후 이미 완료된 receipt 복구 회귀가 race/vet를 통과했습니다. 응답 decode는 fresh value에 수행하여 이전 성공 값이 required field 누락을 가리지 않게 했고 해당 uncertain/recovery 회귀를 추가했습니다. 기존 읽기 CLI race 회귀도 통과했습니다. 실제 create/update/workflow 등 명령 연결은 다음 gh-16 작업 단위입니다.

## gh-16 진행 — 원격 create 기본 필드와 outgoing dependencies

CLI create를 durable RunIntent에 연결했습니다. title/positional type/type-prefix·별칭, priority/points/labels/minor, rich description/acceptance 파일·stdin, due/defer, parent/epic prefix, depends-on 및 CreatedBranch를 서버 생성 요청에 전달합니다. Project의 title 길이 정책을 읽고 검증하며, parent/dependency 해석은 일관된 snapshot에서 수행합니다. Creator Session과 핵심 반환 필드를 검증한 뒤에만 완료·출력합니다. JSON/human 출력 변경은 command identity를 바꾸지 않습니다.

응답 유실 이후 본문 파일이 변경돼도 기존 pending payload/key를 재사용하고 중복 생성하지 않는 회귀가 race/vet를 통과했습니다. 완료 이후 새 실행은 새 key를 쓰며, 잘못된 type/아직 미연결인 incoming --blocks는 생성 전에 거절합니다. 실제 서버/SQLite smoke에서 CLI create의 feature alias/priority/points/minor/본문 보존/parent/dependency 연결을 확인했습니다.

Incoming --blocks는 기존 Issue의 관계도 변경해야 하므로 원자 처리와 복구 검증이 남아 있으며, 이번 기본 생성 단위를 전체 create/gh-16 완료로 분류하지 않습니다. update/delete/restore, workflow, activity/relations/files, boards/notes/ws와 Undo 연결도 계속 진행합니다.

## gh-16 진행 — create --blocks 원자 생성

생성 API에 `blocks: [{issue_id, revision}]`와 `create_blocks` capability를 추가했습니다. 새 Issue와 모든 대상의 관계·revision·review supersession·audit·Undo effect를 한 transaction에 저장합니다. 기존 생성 요청의 fingerprint를 유지하도록 비어 있는 blocks는 직렬화에서 제외합니다. CLI는 일관된 snapshot에서 ID prefix와 revision을 해석하고 immutable intent에 저장합니다.

대상 중 하나의 stale revision, outgoing dependency와의 순환, 중복 대상에서 전체 rollback을 검증했습니다. 같은 key의 정확한 receipt 재생, 생성 Issue 및 복수 대상의 Undo도 HTTP/SQLite 회귀로 검증했습니다. Backend 전체 race/vet, OpenAPI와 GUI 계약 검사, CLI create/init race 및 vet, 실제 서버 CLI smoke가 통과했습니다. 이전 단위에서 미지원이던 incoming --blocks를 연결했으며, gh-16의 나머지 변경 명령은 계속 진행합니다.

## gh-16 진행 — 삭제된 Issue 조회

CLI `deleted`를 원격 목록 API에 연결했습니다. closed를 포함한 전체 페이지를 change token으로 검증한 다음 deleted_at이 있는 Issue만 JSON/일반 출력으로 전달합니다. 복원 준비에 필요한 개별 삭제 Issue 조회도 revision을 보존하는 Client 메서드로 추가했습니다. 조회 실패에서는 부분 결과를 출력하지 않습니다.

Client 전체 race, CLI deleted routing/필터/오류 출력 race, vet 및 실제 Go/SQLite 서버 smoke가 통과했습니다. CLI commit은 `7125239`입니다. 삭제·복원 쓰기 자체와 사유 기록, 복수 Issue의 부분 완료 복구는 아직 미연결입니다.

## gh-16 진행 — 원격 delete/restore 및 사유 원자 기록

CLI `delete`·`restore`를 원격 API와 durable RunIntent에 연결했습니다. 삭제된 Issue까지 포함해 prefix를 해석하고 관측 revision으로 If-Match를 보냅니다. 응답 유실 시 저장한 key·요청 본문으로 receipt를 복구합니다. 여러 Issue 중 실패하면 완료된 ID와 중단 지점을 보고하고, 재실행은 이미 원하는 삭제 상태인 Issue를 건너뜁니다.

API의 선택적 `reason`은 같은 transaction에서 progress log와 Undo effect에 반영되며, 기존 빈 본문 요청의 receipt fingerprint를 보존합니다. 삭제 응답은 `deleted`와 새 revision의 Issue를 함께 반환합니다. 실제 SQLite 서버에서 삭제·복원·사유 log·noop·부분 성공 재실행을 검증했고, 서버 receipt 재생 및 reason fingerprint 충돌도 테스트했습니다.

## gh-16 진행 — update --status workflow 경로

CLI `update --status`를 backend 전용 workflow endpoint에 연결했습니다. ID prefix와 관측 revision으로 immutable intent를 구성하고 응답의 ID·status를 확인합니다. 이미 목표 상태면 noop을 반환합니다. 여러 Issue 중 실패한 경우 완료 범위를 보고합니다. 일반 필드와 status를 함께 지정하면 미연결 다단계 동작을 쓰기 전에 거절합니다.

실제 서버 smoke에서 open→in_progress, 동일 상태 noop, handoff 없는 in_review 정책 거부, 혼합 필드 입력의 무변경을 확인했습니다. status는 generic PATCH를 통과하지 않습니다. CLI vet와 관련 race 회귀를 실행했습니다.

## gh-16 진행 — 일반 update PATCH

CLI `update`의 제목·description/acceptance 및 파일 입력·append·type·priority·points·labels·sprint·parent·due/defer 필드를 backend PATCH에 연결했습니다. 관측 revision과 immutable body/key를 먼저 저장한 뒤 한 번 전송합니다. 응답 유실 후 파일이 바뀌어도 원래 본문을 재전송하는 회귀를 추가했습니다. 이미 요청한 값이면 변경을 생략해 다중 Issue 부분 성공 재실행의 중복 revision 증가를 방지합니다. append는 단일 Issue에 한정하고, 아직 미연결인 comment·relation 및 status와 일반 필드의 혼합은 쓰기 전에 오류로 반환합니다.

실제 SQLite 서버 smoke에서 필드 변경·noop·append·다중 Issue 부분 성공 재실행을 검증했습니다. CLI 관련 race/vet 검증도 통과했습니다.

## gh-16 진행 — start 및 named unstart

CLI `start`와 ID 지정 `unstart`를 backend workflow action에 연결했습니다. 각 Issue의 관측 revision과 요청 key를 저장하며 응답의 ID·상태·implementer Session을 검증합니다. 반복 실행에서는 자기 Session의 진행 중 Issue 또는 claim 없는 open Issue를 noop으로 처리합니다. 여러 Issue 처리 중 실패하면 완료 범위를 보고합니다. `unstart --session/--stale` sweep은 미연결이므로 실행 전 명시적으로 실패합니다.

실제 서버 smoke에서 start→unstart, 반복 noop, blocked Issue의 일반 start 거부와 `--force` start 허용을 검증했습니다. 기존 init 회귀에서 이제 지원하는 start를 미연결 명령 목록에서 제거하고 close guard는 유지했습니다.

## gh-16 진행 — block/unblock/reopen

기존 backend transition adapter를 `block`, `unblock`, `reopen`에 연결했습니다. 명령별 대상 상태를 검증하고 관측 revision/key로 변경합니다. 이미 대상 상태이면 noop으로 처리해 다중 Issue 부분 성공의 재실행을 돕습니다. 실제 서버 smoke에서 block/unblock 및 관리자가 닫은 Issue의 reopen과 각 noop을 확인했습니다.

## gh-16 진행 — comment/comments

CLI `comment`와 `comments add`를 revision/key가 있는 원격 활동 추가 API에 연결했습니다. 결과의 Issue, Session, 본문과 생성시각을 확인한 뒤 intent를 완료합니다. `comments`는 일관된 읽기 snapshot에서 원격 상세의 댓글을 반환합니다. 실제 SQLite 서버 smoke에서 두 추가 경로와 목록·순서를 확인했습니다.
