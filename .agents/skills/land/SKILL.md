---
name: land
description: "Integrate completed work branches and perform authorized delivery after checking the actual changes and validation evidence."
---

# 작업 통합

.codex/conventions/codex-runtime.md와 .codex/conventions/verdict-protocol.md를 읽는다.
사용자가 지정한 작업 단위·대상 브랜치·전달 범위만 처리한다.

1. git status, 작업 브랜치와 실제 커밋을 확인한다. 사용자 변경을 보존한다.
2. 필요한 판정 파일의 대상 커밋·요구 대비·결과를 대조한다.
   검토가 없거나 낡았으면 필요한 검사를 수행하고 그 사실을 기록한다.
3. 승인된 브랜치를 대상 브랜치에 통합한다. 충돌은 의도와 테스트를 확인해 해결한다.
   소스·대상 브랜치 중 미커밋 변경이 있으면 덮어쓰지 않는다.
4. 병합 후 관련 검사를 실행한다. 통합된 HEAD 기준으로 결과를 남긴다.
5. push가 요청 범위면 정확한 원격과 브랜치를 명시한다.
   origin이 없거나 파생 프로젝트 origin이 template/upstream 원본과 같으면 push하지 않는다.
6. PR·이슈 종료·워크트리 정리는 해당 요청/기존 승인 범위 안에서만 수행한다.
   Orca 관리 워크트리는 설치된 Orca 스킬을 따른다.
   워크트리를 정리하기 전 실제 경로·깨끗한 상태·병합 포함 여부를 확인한다.

검증 통과는 외부 쓰기 권한을 새로 만들지 않는다.
이미 승인받은 통합·전달을 반복 확인하지 않는다. 완료 커밋과 검증 결과를 보고한다.
