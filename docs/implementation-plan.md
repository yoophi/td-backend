# td 구현 계획

기준 설계는 [backend architecture](backend-architecture.md), [API 명세](../swagger.json), [ID 정책](id-policy.md), [store 연동 분석](store-integration.md)입니다. 이 계획은 현재 td의 **gh-issue store**를 사용하여 `yoophi/td-backend`에 feature/task로 등록합니다. 등록 당시에는 모두 open 상태였으며, 현재 상태는 td와 [구현 진행 기록](implementation-progress.md)에서 확인합니다.

## Feature

| td issue | Feature | 코드 변경 대상 |
| --- | --- | --- |
| [gh-2](https://github.com/yoophi/td-backend/issues/2) | Go SQLite 기반 HTTP store backend 구현 | 이 저장소의 신규 Go backend |
| [gh-3](https://github.com/yoophi/td-backend/issues/3) | td CLI 원격 store 및 초기화 연동 구현 | yoophi/td의 원격 store adapter와 commands |
| [gh-4](https://github.com/yoophi/td-backend/issues/4) | td monitor 및 serve GUI 원격 연동 구현 | yoophi/td의 monitor/serve 및 td-gui 통합 검증 |

## Task와 의존 관계

Parent는 작업 묶음이며 dependency와 구분합니다. task는 실제 필요한 선행 API/adapter만 의존하고 parent feature 전체가 닫혀야 시작할 수 있는 구조로 만들지 않습니다. 아래 dependency는 `td create --depends-on`으로 등록합니다.

| td issue | Task | Parent | 선행 task |
| --- | --- | --- | --- |
| [gh-5](https://github.com/yoophi/td-backend/issues/5) | Go hexagonal 기반 구성 및 SQLite 저장소 구현 | [gh-2](https://github.com/yoophi/td-backend/issues/2) | 없음 |
| [gh-6](https://github.com/yoophi/td-backend/issues/6) | Project UUIDv7 및 Actor Session 인증 구현 | [gh-2](https://github.com/yoophi/td-backend/issues/2) | [gh-5](https://github.com/yoophi/td-backend/issues/5) |
| [gh-7](https://github.com/yoophi/td-backend/issues/7) | Observed Issue CRUD와 멱등 receipt 구현 | [gh-2](https://github.com/yoophi/td-backend/issues/2) | [gh-6](https://github.com/yoophi/td-backend/issues/6) |
| [gh-8](https://github.com/yoophi/td-backend/issues/8) | Workflow Review Handoff 및 관계 규칙 구현 | [gh-2](https://github.com/yoophi/td-backend/issues/2) | [gh-7](https://github.com/yoophi/td-backend/issues/7) |
| [gh-9](https://github.com/yoophi/td-backend/issues/9) | TDQ Board 및 카드 순서 설정 API 구현 | [gh-2](https://github.com/yoophi/td-backend/issues/2) | [gh-7](https://github.com/yoophi/td-backend/issues/7), [gh-8](https://github.com/yoophi/td-backend/issues/8) |
| [gh-10](https://github.com/yoophi/td-backend/issues/10) | Note 및 다중 Issue WorkSession API 구현 | [gh-2](https://github.com/yoophi/td-backend/issues/2) | [gh-7](https://github.com/yoophi/td-backend/issues/7) |
| [gh-11](https://github.com/yoophi/td-backend/issues/11) | TDQ Query 및 일관된 Monitor projection 구현 | [gh-2](https://github.com/yoophi/td-backend/issues/2) | [gh-8](https://github.com/yoophi/td-backend/issues/8), [gh-9](https://github.com/yoophi/td-backend/issues/9), [gh-10](https://github.com/yoophi/td-backend/issues/10) |
| [gh-12](https://github.com/yoophi/td-backend/issues/12) | Operation audit 조회와 조건부 Undo 구현 | [gh-2](https://github.com/yoophi/td-backend/issues/2) | [gh-8](https://github.com/yoophi/td-backend/issues/8), [gh-9](https://github.com/yoophi/td-backend/issues/9), [gh-10](https://github.com/yoophi/td-backend/issues/10) |
| [gh-13](https://github.com/yoophi/td-backend/issues/13) | Project change token 및 SSE refresh 구현 | [gh-2](https://github.com/yoophi/td-backend/issues/2) | [gh-11](https://github.com/yoophi/td-backend/issues/11) |
| [gh-14](https://github.com/yoophi/td-backend/issues/14) | td init backend 선택 및 Project 연결 구현 | [gh-3](https://github.com/yoophi/td-backend/issues/3) | [gh-6](https://github.com/yoophi/td-backend/issues/6) |
| [gh-15](https://github.com/yoophi/td-backend/issues/15) | Typed HTTP client와 Reader Query dispatch 구현 | [gh-3](https://github.com/yoophi/td-backend/issues/3) | [gh-14](https://github.com/yoophi/td-backend/issues/14), [gh-7](https://github.com/yoophi/td-backend/issues/7), [gh-11](https://github.com/yoophi/td-backend/issues/11) |
| [gh-16](https://github.com/yoophi/td-backend/issues/16) | CLI workflow history 및 Undo 명령 원격 연결 | [gh-3](https://github.com/yoophi/td-backend/issues/3) | [gh-15](https://github.com/yoophi/td-backend/issues/15), [gh-8](https://github.com/yoophi/td-backend/issues/8), [gh-12](https://github.com/yoophi/td-backend/issues/12), [gh-10](https://github.com/yoophi/td-backend/issues/10) |
| [gh-17](https://github.com/yoophi/td-backend/issues/17) | Monitor factory 및 Data Board capability 연결 | [gh-4](https://github.com/yoophi/td-backend/issues/4) | [gh-15](https://github.com/yoophi/td-backend/issues/15), [gh-16](https://github.com/yoophi/td-backend/issues/16), [gh-9](https://github.com/yoophi/td-backend/issues/9), [gh-11](https://github.com/yoophi/td-backend/issues/11), [gh-10](https://github.com/yoophi/td-backend/issues/10) |
| [gh-18](https://github.com/yoophi/td-backend/issues/18) | td serve remote facade 및 SSE relay 구현 | [gh-4](https://github.com/yoophi/td-backend/issues/4) | [gh-15](https://github.com/yoophi/td-backend/issues/15), [gh-16](https://github.com/yoophi/td-backend/issues/16), [gh-13](https://github.com/yoophi/td-backend/issues/13) |
| [gh-19](https://github.com/yoophi/td-backend/issues/19) | CLI Monitor GUI 전체 연동 및 회귀 검증 | [gh-4](https://github.com/yoophi/td-backend/issues/4) | [gh-17](https://github.com/yoophi/td-backend/issues/17), [gh-18](https://github.com/yoophi/td-backend/issues/18), [gh-16](https://github.com/yoophi/td-backend/issues/16), [gh-12](https://github.com/yoophi/td-backend/issues/12) |

## Task 완료 기준

각 GitHub issue/td task의 description과 acceptance에 구현 범위와 검증 기준을 기록했습니다. 기술적 세부사항은 연결된 설계 문서를 기준으로 삼고 scope가 달라지면 acceptance도 갱신합니다.

### Go hexagonal 기반 구성 및 SQLite 저장소 구현 (gh-5)

WAL, FULL, per-connection foreign_keys/busy_timeout/query_only 및 immediate writer transaction을 확인한다. migration checksum과 startup failure가 동작한다. project 격리, rollback, snapshot, stale revision을 실제 Go driver/temporary file로 검증한다.

### Project UUIDv7 및 Actor Session 인증 구현 (gh-6)

Project ID pattern, CSPRNG failure/PK collision, 같은 init key 중복 생성 방지, session ownership과 reader/writer/owner 권한 검사를 검증한다. config에 secret을 노출하지 않고 bootstrap failure 시 listen하지 않는다.

### Observed Issue CRUD와 멱등 receipt 구현 (gh-7)

If-Match 누락 428 및 stale 412, unknown fields 400, 같은 key 중복 effect 방지, 다른 fingerprint 409, receipt 저장 실패 rollback, commit 후 응답 유실과 재시작 후 status/body/ETag replay가 검증된다.

### Workflow Review Handoff 및 관계 규칙 구현 (gh-8)

trusted/delegated/strict/balanced와 minor/creator/implementer/history parity, review basis 무효화, handoff+review transaction, dependency cycle 및 project 간 관계 거부, 다중 Issue cascade 원자성 검증이 통과한다.

### TDQ Board 및 카드 순서 설정 API 구현 (gh-9)

builtin query/name/삭제 제한, position slot과 sparse key 구분, before_id 이동, 중간 UNIQUE 충돌 방지, board If-Match와 project 간 card 연결 거부를 검증한다.

### Note 및 다중 Issue WorkSession API 구현 (gh-10)

soft deletion, note revision, owning Session의 tagging/end 권한, 종료 후 수정 제한, empty tags/clear, project 간 issue tagging 거부 및 change counter가 검증된다.

### TDQ Query 및 일관된 Monitor projection 구현 (gh-11)

기본 목록이 closed만 제외하고 전체 필드를 반환한다. records/total/token snapshot 일치, N+1 방지, monitor ready/reviewable/rework/close 분류와 available_transitions 일치, session focus/presence, TDQ 오류가 검증된다.

### Operation audit 조회와 조건부 Undo 구현 (gh-12)

모든 affected aggregate의 after-revision을 검사해 후속 변경이 있으면 409를 반환한다. 다중 effect undo가 원자적이고 revision은 증가하며 review/history를 삭제하거나 기존 승인을 되살리지 않는다.

### Project change token 및 SSE refresh 구현 (gh-13)

Last-Event-ID, refresh/ping payload, reconnect 후 재조회, receipt replay의 token 유지, heartbeat, 느린 subscriber 및 shutdown 검증이 통과한다. SSE가 DB transaction/connection을 장시간 점유하지 않는다.

### td init backend 선택 및 Project 연결 구현 (gh-14)

ID 없는 최초 init은 생성하고 연결이 있으면 재사용한다. 명시한 Project ID는 형식/접근 검증을 거친다. 충돌 연결을 자동 덮어쓰지 않고 유실 응답/저장 실패 뒤 같은 key로 회복한다. worktree config association을 유지한다.

### Typed HTTP client와 Reader Query dispatch 구현 (gh-15)

Reader가 모든 page를 수집하고 token 불일치 시 restart한다. stale/uncertain write는 성공으로 보고하지 않는다. SQLite 접근과 GitHub 오분기를 차단하고 취소·backend 오류 mapping을 검증한다.

### CLI workflow history 및 Undo 명령 원격 연결 (gh-16)

일반 status PATCH로 정책을 우회하지 않고 Session attribution을 유지한다. multi-Issue 부분 성공을 표시하며 동일 intent retry를 유지한다. GUI subprocess에서 쓰는 td query/unstart/update --status도 remote dispatch를 사용한다.

### Monitor factory 및 Data Board capability 연결 (gh-17)

실패 시 이전 화면을 유지하고 refresh 중복 요청을 막는다. 요청 context 변경 시 오래된 observation 쓰기를 거부한다. 개인 pane/filter/cursor는 local 유지, session focus 및 board/issue operations는 원격 반영되며 polling이 동작한다.

### td serve remote facade 및 SSE relay 구현 (gh-18)

api/td-serve.swagger.json의 GUI 필수 routes/envelope/fields가 유지된다. browser credentials 노출 없이 refresh/ping을 relay한다. browser If-Match를 보존하고 미전달 stale UI 한계를 문서화한다. proxy buffering/cancel/shutdown을 검증한다.

### CLI Monitor GUI 전체 연동 및 회귀 검증 (gh-19)

신규 init에서 create/start/handoff/review/close/undo, board 정렬/notes/ws, 두 Session review 정책, GUI SSE/reconnect와 response 유실 재송신이 end-to-end 통과한다. go test ./..., OpenAPI, 실제 SQLite integration checks 결과를 기록한다.

## 착수 및 종료

첫 착수 대상은 Go 구성/SQLite 저장소 task입니다. 이후 `td ready`의 선행 dependency 완료 상태를 확인합니다.

```sh
td list --all
td ready
td show <task-id>
td start <task-id>
td log "구현 내용과 검증 결과"
td handoff <task-id> --done "완료한 내용" --remaining "남은 내용"
td review <task-id>
```

구현·필요한 검증을 완료한 작업만 review로 제출합니다. approve에는 실제 reviewer 또는 명시적 self-review와 reason을 기록합니다. 구현을 수행하지 않은 Session을 독립 reviewer로 이름만 기재하지 않습니다. 각 완료 task는 별도로 commit/push하고 완료 알림을 보냅니다.

처음부터 capability 전체를 지원한다고 응답하지 않습니다. backend 단계별로 완료된 capability를 공개하고, 최종 통합 task에서 CLI lifecycle, monitor 편집·보드·메모, serve/GUI HTTP·SSE 및 CLI-backed query/status를 함께 검증합니다. import/export·기존 store migration 및 sync 프로토콜은 초기 범위 밖입니다.
