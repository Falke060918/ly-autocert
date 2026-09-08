# 통합 파생 프로젝트 초기화

새 프로젝트 초기화 요청이 있을 때 메인이 이 런북을 읽고 하나의 implementer에게 위임한다.
마커만으로 삭제·외부 연결이 승인되지는 않는다.

1. 루트·변경 파일·원격·양쪽 identity를 확인한다. 템플릿 원본이 아닌 파생 사본에서 진행한다.
2. 기존 파일과 Git 이력을 보존한다. origin이 템플릿이면 주소를 보존하고 template으로 이름을 바꾼다.
   이미 template이 있으면 대조하고 덮어쓰지 않는다. 원본 추적 원격의 push는 차단한다.
3. .claude/identity와 .codex/identity를 함께 identity: project, setup: pending으로 갱신한다.
   template:에 실제 통합 템플릿 경로/URL을 기록하고 notion: pending으로 둔다.
4. 요청에서 이름·목적을 얻는다. 없으면 목적은 dev, prefix는 프로젝트 이름에서 짧은 대문자 영숫자로 정해 알린다.
   기존 제품의 목적을 임의 변경하지 않는다. 목적은 dev/docs/research/test/mixed 중 하나다.
5. STATE를 새 프로젝트의 목적과 다음 단계로 작성한다. 기존 프로젝트 기록은 보존·통합한다.
   MAP/GOTCHAS/constitution의 프로젝트 사실을 확인한다. 역사 자료를 승인 없이 삭제하지 않는다.
6. GitHub·Notion·MCP는 요청된 경우에만 연결한다. 사용자 전역 설정·인증·hook trust·권한은 바꾸지 않는다.
7. 스택·명령·불변 원칙은 확인한 사실만 constitution/MAP에 반영한다. 새로운 기술 선택은 architect에서 정한다.
8. 양쪽 identity에 setup: done과 동일한 purpose/prefix/template을 기록한다.
   origin은 현재 원격의 정규화 지문이며 없으면 none이다.
   python scripts/validate.py, python scripts/starter.py status, bash scripts/session-markers.sh로 확인한다.
   변경한 파일만 커밋하며 외부 반영은 승인된 범위에서만 한다.
9. 결과·가정·다음 단계를 알린다. 이미 받은 목적·아이디어는 다시 묻지 않는다.
   아직 목표가 없으면 목적과 아이디어를 짧게 확인한다. 신규 제품은 kickoff,
   문서는 kickoff-docs, 조사는 kickoff-research로 이어간다. 기존 제품은 처음부터 재기획하지 않는다.

양쪽 identity의 공통 예시:

```text
identity: project
setup: done
origin: none
template: https://github.com/Falke060918/duet-starter.git
notion: pending
work-order-prefix: APP
purpose: dev
```

도구를 바꾸기 전에 handoff로 STATE를 저장한다. automode·세션 로그는 도구별로 유지한다.
