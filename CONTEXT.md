# td 작업 관리

프로젝트 안에서 여러 작업 세션이 이슈를 구현·인계·검토하고 완료하는 도메인입니다.

## Language

**Project**: 이슈, 보드, 작업 이력과 review policy를 공유하는 작업 공간입니다. 표시 이름이 같아도 서로 다른 Project일 수 있습니다.
_Avoid_: directory, repository를 Project의 동의어로 사용

**Actor**: 프로젝트에 접근하고 작업 세션을 소유하는 인증된 주체입니다. 사람이거나 자동화 주체일 수 있습니다.
_Avoid_: Session과 혼용

**Session**: 특정 agent·branch·context에서 작업을 수행하고 이력에 기록되는 작업 정체성입니다. 하나의 Actor가 여러 Session을 소유할 수 있습니다.
_Avoid_: 로그인 세션

**Issue**: 제목, 완료 조건, 작업 상태와 구현·검토 이력을 가진 관리 대상 작업입니다. bug, feature, task, epic, chore를 포함합니다.
_Avoid_: GitHub issue만을 의미하는 사용

**Implementation involvement**: Session이 이슈 구현에 참여했다는 누적 이력입니다. 현재 구현 담당을 해제해도 과거 참여는 사라지지 않습니다.
_Avoid_: 현재 implementer만으로 참여 여부를 판단

**Handoff**: 완료한 일, 남은 일, 결정과 불확실성을 다음 Session에 전달하는 구조화된 인계입니다.
_Avoid_: 일반 댓글

**Review**: 이슈의 특정 검토 기준에 대해 남긴 승인 또는 변경 요청입니다. 검토 기록과 이슈 종료는 별개의 행위입니다.
_Avoid_: closed 상태의 동의어

**Review basis**: 검토가 판단한 작업 내용과 인계의 조합입니다. 관련 작업 내용이나 인계가 바뀌면 기존 승인은 종료의 근거가 될 수 없습니다.
_Avoid_: 가장 최근 승인이라는 이유만으로 유효성을 인정

**Board**: 질의 조건과 명시적 카드 순서로 이슈를 보여주는 공유 작업 뷰입니다. 카드 순서는 이슈 자체의 priority와 별개입니다.
_Avoid_: 이슈의 소유 컨테이너

**Focus**: 한 Session이 현재 주목하는 이슈입니다. 같은 Project의 다른 Session에 같은 Focus를 강제하지 않습니다.
_Avoid_: 프로젝트 전체의 현재 작업

**Work session**: 하나의 Session이 여러 이슈를 묶어 수행하는 시작·종료가 있는 작업 묶음입니다.
_Avoid_: Session과 혼용

**Activity**: 댓글, log, handoff 등 이슈를 이해하는 데 필요한 공유 작업 이력입니다. 과거 자료를 가져온 기록만으로 현재 review가 성립하지 않습니다.
_Avoid_: 승인 권한의 증거

**Operation**: 하나의 작업 의도로 수행한 변경과 그 결과를 식별하는 기록입니다. 재전송해도 같은 변경 결과를 확인할 수 있습니다.
_Avoid_: HTTP 호출 횟수와 혼용
