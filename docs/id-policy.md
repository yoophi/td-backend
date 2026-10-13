# Project ID와 operation key 생성 규칙

이 문서는 신규 backend의 Project ID와 client operation key에 대한 설계 계약입니다. 기존 td Issue/Board/Session ID를 다른 형식으로 바꾸는 결정은 포함하지 않습니다.

## Project ID

| 항목 | 규칙 |
| --- | --- |
| 생성 주체 | backend application의 Project 생성 use case |
| 형식 | `prj_` + 소문자 canonical UUIDv7, 총 40자 |
| 예시 | `prj_019a7a6e-8c00-7d42-9f23-8c5e7a1b6d90` |
| 생성 시점 | 최초 `POST /v1/projects` transaction 안에서 생성 |
| 저장 | SQLite projects.id의 TEXT PK와 client `.todos/config.json`의 backend.project_id |
| 변경 / 재사용 | 생성 후 불변; 이름·repository 변경에도 유지; 삭제된 Project ID를 새 Project에 재사용하지 않음 |
| 연결 identity | `(backend URL, project ID)` |

UUIDv7 부분은 [RFC 9562 §5.7](https://www.rfc-editor.org/rfc/rfc9562.html#section-5.7)을 따릅니다. 서버 UTC Unix millisecond timestamp 48bit, version/variant bits, 나머지 74bit의 cryptographically secure randomness를 사용합니다. Go 구현에서는 검증된 UUIDv7 구현과 `crypto/rand`를 사용하고, domain/application에는 ID 생성 함수를 주입하여 테스트에서 고정합니다.

같은 millisecond에 여러 ID가 생성될 수 있습니다. ID의 엄격한 생성 순서나 commit 순서는 보장하지 않으며 정렬은 created_at과 ID tie breaker를 사용합니다. 난수 획득 실패 시 Project 생성 전체를 중단합니다. DB의 Project PK 충돌만 새 후보로 최대 세 번 시도하고, 실패하면 rollback합니다. 다른 FK/UNIQUE/policy 실패에서는 새 Project를 만들기 위해 command를 자동 재실행하지 않습니다.

Project ID는 이름·Git remote·로컬 경로의 hash 또는 directory basename에서 파생하지 않습니다. 클라이언트는 UUID timestamp를 업무 의미로 해석하지 않고 opaque 값으로 다룹니다. ID를 알고 있어도 권한이 생기지 않으며 모든 조회·변경은 인증과 membership 검사를 거칩니다.

허용 pattern:

```regex
^prj_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$
```

API는 완전한 canonical ID만 허용합니다. 잘못된 형식은 400이며 bare UUID, 대문자, 축약 ID, 이름 자동 해석을 허용하지 않습니다. 유효한 형식이지만 존재하지 않는 ID는 404, 접근 권한이 없으면 403입니다. 기존 프로젝트 연결 옵션은 아직 미구현인 `--project-id`로 예정합니다.

## 초기화 / 연결

```sh
# 신규 프로젝트: backend가 ID를 생성하고 td가 저장
td init --store=td-backend,http://localhost:7000

# 기존 프로젝트 연결: td는 새 ID를 생성하지 않음
td init --store=td-backend,http://localhost:7000 \
  --project-id=prj_019a7a6e-8c00-7d42-9f23-8c5e7a1b6d90
```

저장된 연결이 있는 상태에서 ID 없이 init을 반복하면 기존 ID를 검증하고 재사용합니다. 명시한 URL/ID가 저장된 연결과 다르면 자동으로 덮어쓰지 않고 별도 연결 변경 절차를 안내합니다. 독립 checkout에서 같은 Project를 공유하려면 전체 ID를 지정합니다. git worktree는 td root 연결을 통해 기존 config를 공유합니다.

Project 생성 body에는 `id` 필드가 없습니다. 서버가 같은 init 요청을 반복해서 받으면 아래 operation key의 receipt를 통해 **같은 Project ID**를 반환합니다. 응답 유실이나 config 저장 실패 후 init을 다시 실행해도 새 Project를 만드는 일이 없어야 합니다.

## Operation key / init key

Operation key는 Project ID와 별개의 값입니다. client(td CLI adapter 또는 td serve facade)가 하나의 command intent에 대해 소문자 canonical **UUIDv4** 36자를 생성하여 `Idempotency-Key`로 보냅니다. UUIDv4는 [RFC 9562 §5.4](https://www.rfc-editor.org/rfc/rfc9562.html#section-5.4)를 따르며 CSPRNG를 사용합니다.

```text
f47ac10b-58cc-4372-a567-0e02b2c3d479
```

최초 init key는 Project 생성 요청 전에 로컬에 영속화합니다. 같은 intent의 retry에는 원래 key·body·If-Match를 유지하며 새 intent에는 새 key를 발급합니다. Project 생성은 `(Actor, @server, key)`, 일반 변경은 `(Actor, Project ID, key)`로 유일성을 검사합니다. 동일 key의 다른 fingerprint는 409이며 성공 receipt는 Project 수명 동안 유지합니다.

API의 Operation.id와 operation 조회 path의 operation_id는 이 UUIDv4입니다. DB의 operations.row_id는 server-generated UUIDv7 canonical 문자열이며 외부 operation ID로 노출하지 않습니다. 내부 row ID와 client key를 바꾸어 사용하지 않습니다. X-TD-Session과 context_id도 operation key가 아니며 기존 session identity 계약을 유지합니다.

허용 operation pattern:

```regex
^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$
```

OpenAPI의 ProjectID와 OperationKey component에 길이, pattern, example, 설명을 반영했습니다. SQLite DDL 초안은 PK/receipt 유일성을 담당하며 UUID 형식과 CSPRNG 생성은 application 및 HTTP adapter가 검증합니다. 실제 생성 코드와 runtime 검증은 구현 계획의 identity task에서 진행합니다.
