# td-backend 통합 검증 (2026-10-14)

검증 대상은 `td-backend` main의 `8a1e11e` 기반 Go/SQLite HTTP 서버와 `td`의 `feat/td-backend-store` 브랜치 `39d519e`입니다. 통합 중 발견한 init JSON capability 표시는 후속 `1dd7e57`에서 수정하고 대상 테스트·설치를 다시 실행했습니다. 새 임시 Git 디렉터리에서 `td init --store=td-backend,http://127.0.0.1:<port>`를 실행해 Project ID를 서버에서 발급받았고, 로컬 이슈 SQLite 없이 CLI/Monitor/GUI 경로를 확인했습니다. 아래 ID는 임시 검증 Project의 리소스입니다.

| 경로 | 확인 결과 |
| --- | --- |
| Project init | 새 Project ID `prj_01a13a94-a5ab-709a-8541-506a4e858c33` 발급·저장, 다른 checkout의 `--project-id` 연결 성공 |
| Issue lifecycle | create → start → 구조화 handoff → review 성공. 구현 Session의 일반 approve는 403, 별도 reviewer Session의 approve는 closed·reviewer attribution 기록. 별도 Issue의 `close`도 성공 |
| Undo | `update --title` 후 `last`가 원래 operation을 표시. `undo` 후 원래 제목 복원, revision 증가 확인 |
| Board | TDQ board 생성, 두 카드 중 하나를 1번 위치로 이동, `board show`의 첫 카드 순서 확인 |
| Note | add → edit → pin → show에서 새 내용과 pinned 상태 확인 |
| Work Session | start → issue tag → decision log → current → end → show; log가 Issue와 Work Session에 함께 귀속됨 |
| Monitor | 원격 project의 terminal `td monitor` 실행·종료, `/v1/monitor` snapshot의 ready/blocked/activity/active_sessions 확인 |
| GUI subprocess | `td-gui`가 원격 `td serve`를 시작한 상태에서 `/gui/query?q=status = open`이 대상 ID를 반환하고 `/gui/status`가 blocked 변경을 적용함 |
| GUI SSE | `/v1/events` 연결 중 두 차례 CLI 변경마다 `refresh` 수신. `Last-Event-ID`를 보낸 재연결에서도 이후 변경을 수신 |
| 응답 유실 복구 | 별도 HTTP 프록시가 backend의 성공한 PATCH 응답을 잘라 CLI에 전달. 첫 명령은 uncertain write를 보고하고 동일 명령 재실행은 저장된 key로 성공. 실제 Issue revision `r-5`→`r-6`으로 한 번만 증가 |
| 복합 update / claim sweep | 필드·status·comment·dependency 동시 변경, 의존성 교체·역방향 blocks, 다중 대상 누락 ID skip 확인. 거부된 status 전이는 409로 남고 앞선 필드 변경과 댓글 수는 재실행 뒤에도 중복되지 않음. `--session --force`, `--stale 100ms --force`가 각각 claim을 해제함 |

백엔드 `make test`, `make check`, `make spec` 통과: OpenAPI 54 paths/69 operations, serve 47 paths, GUI contract 30 route/method pairs와 SQLite schema/rollback 검사 통과. CLI는 `go test ./cmd -run '^TestBackend'`, 해당 회귀의 race 검사, `go vet ./cmd ./internal/backendstore`, `make install`을 통과했습니다. CLI 전체 `make test`는 최종 실행 결과를 기록할 예정입니다.

브라우저의 실제 화면 렌더링은 이 환경에 사용 가능한 브라우저 surface가 없어 수행하지 않았습니다. GUI HTTP 계약, subprocess와 SSE는 실행 중인 `td-gui`에 직접 요청해 검증했습니다.
