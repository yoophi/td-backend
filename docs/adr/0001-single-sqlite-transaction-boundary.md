# 모든 프로젝트와 operation receipt를 하나의 SQLite 파일에 저장

Go hexagonal architecture의 SQLite adapter는 모든 Project, 작업 이력, review와 operation receipt를 하나의 파일에 저장합니다. 프로젝트 생성의 멱등성부터 여러 이슈에 걸친 cascade/undo까지 같은 트랜잭션으로 처리해야 하므로, 프로젝트별 파일과 별도 audit DB를 나누는 방식 대신 공통 DB와 project-scoped 복합 키를 선택했습니다.

각 Project의 데이터 접근에는 project ID가 반드시 포함됩니다. 파일 전체가 하나의 writer를 공유하므로 초기 배포는 단일 서버 프로세스와 로컬 persistent volume으로 제한하며, 높은 쓰기 처리량이 필요해지면 application port를 유지하면서 저장 adapter를 교체합니다.
