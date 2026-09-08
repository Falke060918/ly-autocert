# 셸 함정

- Windows PowerShell에서는 UTF-8을 명시해서 읽는다. 파일 편집은 apply_patch를 사용한다.
- PowerShell 5.1의 Set-Content -Encoding utf8은 BOM을 넣을 수 있다. JSON·TOML·셸 파일의 바이트를 확인한다.
- PowerShell에서 Bash 문법, cmd에서 PowerShell 문법을 섞지 않는다. Bash 보조 스크립트는 Git Bash/WSL에서 실행한다.
- 셸 코드에 JSON.stringify 결과를 넣는 것은 escaping이 아니다. 백틱·$()·따옴표가 실행될 수 있다.
- 검색은 rg를 우선하고 숨김 설정을 찾을 때 rg --hidden을 쓴다. 0건이면 검색 범위를 확인한다.
- $HOME/$home/$CODEX_HOME을 임시 변수로 재사용하지 않는다.
- 시간·설정·기록은 실제 도구 결과로 확인한다. 네트워크 실패를 앱 종료로 단정하지 않는다.
- Codex 세션은 response_item/payload, session_meta를 포함한 rollout 형식이다.
  Claude stream-json의 tool_use·parent_tool_use_id 파서를 재사용하지 않는다.
- 권한 실패는 실행기 정책에 따른다. 훅이나 다른 에이전트로 동일 작업을 우회하지 않는다.
- 출력이 크면 필터링하거나 사용 가능한 rtk를 명시적으로 호출한다. Claude 자동 rewrite 훅은 가정하지 않는다.
