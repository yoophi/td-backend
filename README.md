# td-backend

`td`의 세 번째 store인 HTTP backend를 위한 **API 및 backend 설계**입니다. Go · SQLite · hexagonal architecture를 사용하며 `td cli`, `td monitor`, `td serve`를 거쳐 사용하는 `td-gui`를 대상으로 합니다. 서버 및 CLI adapter 구현은 아직 포함하지 않습니다.

목표 사용법:

```sh
td init --store=td-backend,http://localhost:7000
td create "Implement the remote store adapter"
td monitor
td serve
# 같은 프로젝트에서 td-gui 실행
td-gui
```

현재 `td`는 위 init 문법을 지원하지 않습니다. 아래 명세에 맞춘 backend와 adapter 구현 후 사용할 수 있는 목표 흐름입니다.

| 파일 | 용도 |
| --- | --- |
| [swagger.json](swagger.json) | 원격 backend: 기본 URL `http://localhost:7000`, 49개 path / 63개 operation |
| [api/td-serve.swagger.json](api/td-serve.swagger.json) | 로컬 serve facade: 기존 GUI의 `/v1/*`, `/health` 계약 유지 |
| [docs/api-design.md](docs/api-design.md) | endpoint 그룹, 원자성, session, monitor 및 GUI 연결 설계 |
| [docs/backend-architecture.md](docs/backend-architecture.md) | Go package 구성, ports/adapters, domain, SQLite transaction 및 운영 설계 |
| [docs/sqlite-schema.sql](docs/sqlite-schema.sql) | 실행 검증 가능한 SQLite 저장 schema 설계 초안 |
| [CONTEXT.md](CONTEXT.md) | 도메인 용어 |
| [docs/store-integration.md](docs/store-integration.md) | 실제 td 소스 분석과 adapter 수정 지점 |
| [api/model-schemas.json](api/model-schemas.json) | 분석한 commit의 Go DTO / domain model에서 추출한 schema snapshot |

JSON은 [OpenAPI 3.0.3](https://spec.openapis.org/oas/v3.0.3) 형식이며 Swagger Editor/UI에서 열 수 있습니다. `x-status: proposed`, operation의 `x-origin`, 모델의 `x-source`가 신규 설계와 원본 코드의 관계를 표시합니다. `adapted`는 기존 기능을 참고했다는 뜻이며 기존 서버와 wire-compatible하다는 뜻은 아닙니다.

재생성 및 검증:

```sh
python3 scripts/generate_openapi.py
python3 -m venv .venv
.venv/bin/pip install -r requirements-dev.txt
.venv/bin/python scripts/validate_openapi.py
python3 scripts/validate_sqlite_design.py
```

검증은 OpenAPI 구조·참조와 GUI에서 필요한 route/응답 계약을 확인합니다. 실행 중인 서버에 대한 통합 검증은 서버와 adapter 구현 이후 별도로 필요합니다.
