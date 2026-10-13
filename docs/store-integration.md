# td-backend store 연동 분석

분석 대상: [`yoophi/td` commit `c64d7873`](https://github.com/yoophi/td/tree/c64d7873d8f555dd49875e34e6b39ae5257e2e0c). 아래의 현재 동작은 소스에서 확인한 사실이며, 신규 구성 및 초기화 흐름은 구현 제안이다.

## 현재 지원 범위

`td init --store=td-backend,http://localhost:7000`은 현재 실행할 수 없다. `--store`는 단일 문자열이고, `prepareProjectStore`와 `config.Store`는 정확히 `sqlite` 또는 `gh-issue`만 받는다. 쉼표로 store 종류와 URL을 분리하는 파서는 없다. [cmd/init.go:31](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/cmd/init.go#L31), [cmd/config_store.go:16](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/cmd/config_store.go#L16), [internal/config/store.go:15](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/internal/config/store.go#L15)

설정은 `<project>/.todos/config.json`에 기록된다. 현재 필드는 `store`와 `github: {remote, repo}`이며 backend URL 또는 원격 project ID 필드는 없다. `SetStore`는 unrelated 설정을 보존하고 잠금 안에서 원자적으로 저장한다. 네트워크 검증은 이 잠금 밖에서 수행해야 한다. [internal/models/models.go:285](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/internal/models/models.go#L285), [internal/config/config.go:13](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/internal/config/config.go#L13), [internal/config/store.go:27](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/internal/config/store.go#L27), [internal/config/store.go:48](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/internal/config/store.go#L48)

GitHub init은 remote/repository를 검증한 뒤 config만 기록하고 반환한다. SQLite init은 DB와 session을 생성한다. 따라서 신규 원격 store 분기는 SQLite DB 초기화보다 먼저 반환하는 구조가 적합하다. store 전환은 현재 데이터를 자동 이관하지 않는다. [cmd/init.go:45](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/cmd/init.go#L45), [cmd/init.go:70](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/cmd/init.go#L70), [cmd/config_store.go:49](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/cmd/config_store.go#L49)

## 기존 HTTP API를 그대로 사용하면 생기는 차이

기존 `td serve`는 이미 `GET /health`, `GET /v1/project`, issue CRUD, workflow, comments, dependencies, boards, sessions, stats, labels, events API를 제공한다. 그러나 한 로컬 프로젝트를 서버 시작 시 선택한다. project 생성/선택 endpoint와 여러 프로젝트를 구분하는 path가 없다. `/v1/project`의 `name`은 디렉터리 basename이며 `path`는 서버 로컬 경로다. 영속적인 원격 project identity가 아니다. [internal/serve/server.go:179](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/internal/serve/server.go#L179), [internal/serve/project.go:9](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/internal/serve/project.go#L9)

서버는 하나의 web session을 생성하고 모든 요청에 사용한다. 이 방식으로 여러 CLI/agent가 접속하면 implementer와 reviewer가 같은 session으로 기록되어 review 독립성 및 attribution이 달라진다. backend store에서는 요청별 client session을 별도로 해결해야 한다. [cmd/serve.go:77](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/cmd/serve.go#L77), [cmd/serve.go:136](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/cmd/serve.go#L136), [internal/serve/handlers_transitions.go:120](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/internal/serve/handlers_transitions.go#L120)

## 권장 초기화 및 프로젝트 식별

아래는 신규 backend 설계 제안이며 기존 endpoint로 구현된 동작이 아니다.

1. `--store`를 첫 쉼표에서 종류와 URL로 분리하고, URL의 scheme/host를 검증한다. config의 `store`에는 `td-backend`만 저장한다.
2. 기존 연결이 있으면 저장된 project ID를 조회하고 검증한다. 반복 init은 같은 프로젝트를 재사용한다.
3. 최초 init에서는 client가 안정적인 initialization key를 로컬에 먼저 기록한 뒤 project 생성 요청을 보낸다. 서버는 같은 key를 재사용하면 동일 프로젝트를 반환한다. 네트워크 응답 유실이나 config 저장 실패 뒤 재실행해도 중복 생성하지 않는다.
4. 원격 응답의 영속적인 project ID와 API URL을 기존 config 잠금/원자적 저장 방식으로 기록한다. 프로젝트 이름은 표시용이며 경로 basename으로 유일성을 판단하지 않는다.
5. 다른 컴퓨터 또는 독립 checkout에서 같은 프로젝트에 연결할 때는 project ID를 지정하는 명시적 연결 옵션이 필요하다. worktree는 기존 `.td-root`/association/main-worktree 탐색을 통해 같은 config를 공유할 수 있다. [internal/workdir/workdir.go:57](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/internal/workdir/workdir.go#L57)

권장 저장 예시:

```json
{
  "store": "td-backend",
  "backend": {
    "url": "http://localhost:7000",
    "project_id": "project-example"
  }
}
```

원격 식별은 `(backend URL, project ID)` 조합으로 한다. Git remote는 선택적 metadata로 사용할 수 있지만 GitHub 인증이나 Git repository를 필수로 요구하지 않는다. backend URL에 자격 증명을 넣어 저장하지 않는다.

세션은 client/worktree/branch/agent context별 identity를 서버에 등록하고 반환된 session ID로 요청한다. focus와 active work session 같은 context는 프로젝트 전체의 단일 값으로 공유하지 않는다. 기존 GitHub context가 hostname, repository, worktree, branch, agent fingerprint와 `TD_CONTEXT_ID` 등을 조합하여 device-local state를 분리하는 점을 참고할 수 있다. [internal/ghcontext/context.go:22](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/internal/ghcontext/context.go#L22), [internal/ghcontext/context.go:54](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/internal/ghcontext/context.go#L54)

## CLI 수정 지점

| 수정 지점 | 필요한 변경 |
| --- | --- |
| `cmd/init.go`, `cmd/config_store.go`, `cmd/config.go` | backend 선택문법, health/project 검증, 원격 init/연결, JSON 결과 |
| `internal/models/models.go`, `internal/config/store.go` | backend config 타입, store 검증 및 설정 저장 |
| `internal/issuestore/reader.go` | backend Reader 분기; 현재 GitHub 외 종류는 SQLite로 진행함 |
| `cmd/gh_issue.go` 및 command별 dispatch | 현재 SQLite 외 종류를 GitHub로 보내는 분기를 명시적 store switch로 변경 |
| `internal/db/db.go` | 모든 비 SQLite store의 SQLite 접근 차단; 현재 GitHub만 명시적으로 차단 |
| session/context 및 monitor/serve factory | per-client remote session과 backend adapter |

근거: [internal/issuestore/reader.go:32](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/internal/issuestore/reader.go#L32), [cmd/gh_issue.go:25](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/cmd/gh_issue.go#L25), [internal/db/db.go:78](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/internal/db/db.go#L78), [cmd/serve.go:73](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/cmd/serve.go#L73)

`Reader`와 `IssueWriter`는 작은 공통 계약이며 모든 store 기능을 포괄하지 않는다. workflow, reviews, relations, history는 별도의 의미와 원자성 요구가 있다. REST의 generic PATCH만으로 start/review/approve 등을 치환하지 말고 해당 작업을 서버에서 검증하고 한 트랜잭션으로 처리해야 한다. [internal/issuestore/reader.go:23](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/internal/issuestore/reader.go#L23), [internal/issuestore/writer.go:10](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/internal/issuestore/writer.go#L10)

## td monitor 지원 경계

`td monitor`는 현재 GitHub 전용 factory와 SQLite factory로 나뉜다. embedded monitor 생성 경로도 별도의 store 분기를 가지므로 CLI entrypoint만 수정하면 충분하지 않다. backend model factory를 두 경로에 추가해야 한다. [cmd/monitor.go:55](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/cmd/monitor.go#L55), [pkg/monitor/model.go:445](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/pkg/monitor/model.go#L445)

SQLite의 `FetchDataWithSearchMode`는 DB handle을 직접 사용하여 session, focus, in-progress, activity, 분류된 task list, 최근 handoff, active sessions를 조합한다. `OpenReader`의 issue Get/List만 HTTP로 바꾸는 것으로는 monitor를 지원할 수 없다. backend의 dashboard endpoint가 동일 정보를 실제 요청 session 기준으로 계산하고 adapter가 `RefreshDataMsg`로 변환하는 방식이 적합하다. refresh 실패 시 이전 화면을 유지해야 한다. [pkg/monitor/data.go:159](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/pkg/monitor/data.go#L159), [pkg/monitor/github_data_source.go:17](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/pkg/monitor/github_data_source.go#L17)

이미 존재하는 `Model.DataSource`와 `Model.BoardSource`가 원격 adapter 삽입 지점이다. `fetchData`는 DataSource가 있으면 SQLite fetch를 호출하지 않는다. detail/stats/handoffs/autofill, create/edit/delete, transition/approval/review, notes, board editor/preview/move/visit/view 등의 capability interface도 구현해야 읽기뿐 아니라 monitor의 버튼과 modal이 동작한다. 일부 interface와 값은 `ghstore.Record`에 결합되어 있어 backend용 공통 observation/revision 타입으로 정리하는 작업이 필요하다. [pkg/monitor/model.go:53](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/pkg/monitor/model.go#L53), [pkg/monitor/model.go:1398](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/pkg/monitor/model.go#L1398), [pkg/monitor/github_data.go:19](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/pkg/monitor/github_data.go#L19)

권장 monitor adapter는 하나의 request context/cancel을 소유하고 refresh 중복 실행을 막으며, actor identity와 project config가 변경되면 오래된 요청/관측값의 쓰기를 거부한다. pane 높이, search/filter, cursor, 개인 board view/last-visited preference는 로컬 상태로 유지할 수 있고, 공유 board query/position과 issue 데이터는 backend에 저장한다. focus/active work session은 실제 actor scope로 유지한다. 기존 remote factory의 구성이 참조점이다. [pkg/monitor/github_model.go:78](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/pkg/monitor/github_model.go#L78), [pkg/monitor/github_model.go:116](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/pkg/monitor/github_model.go#L116)

현재 terminal monitor는 interval에 따른 DataSource refresh를 사용한다. `change_token` 및 SSE `refresh`/`ping`은 기존 HTTP server 측 기능이므로 terminal monitor가 이를 이미 소비한다고 가정하면 안 된다. backend adapter는 우선 polling으로 동작할 수 있고, SSE 이벤트 수신 시 dashboard를 다시 읽도록 확장할 수 있다. token은 변경 여부를 나타내는 opaque 값이며 요청별 actor 분류나 revision precondition을 대체하지 않는다. SSE는 완전한 history replay가 아니라 화면 갱신 신호로 취급한다. [pkg/monitor/github_data_source.go:62](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/pkg/monitor/github_data_source.go#L62), [internal/serve/sse.go:26](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/internal/serve/sse.go#L26), [internal/serve/github_events.go:124](https://github.com/yoophi/td/blob/c64d7873d8f555dd49875e34e6b39ae5257e2e0c/internal/serve/github_events.go#L124)
