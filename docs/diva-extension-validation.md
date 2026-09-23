# 가희 추가 기능 검증 및 남은 범위

2026-09-23. 서버 코드만 변경했습니다. 운영 DB, 실행 중인 서버, 클라이언트
파일과 config.json은 수정하지 않았으며 배포·커밋도 하지 않았습니다.

## 연결한 기능

- [선율](diva-melody-implementation.md): 명시적 보수 수령, 계정/회차별 최고
  획득량과 사용량 분리, 동시 차감 보호. 교환품은 후속 조사로 42종에서
  [76종](diva-melody-catalog-restoration.md)으로 확장했으며 품목별 수량 추론을 구분합니다.
- [요격전 로그](diva-tactics-log.md): 실제 점수 기여·에리어 확보·보물 발견,
  현재 수렵단의 이력만 최대 200행으로 전송. 후속 보완으로 개인 누적 10,000점마다
  원본 kind 2 돌파 표시를 추가했습니다. 기준 주기는 서버 운영용이며 점수/보수에는 영향이 없습니다.
- [특별방](diva-special-hall.md): 기존 실제 입장 플래그와 1에리어 이용권 안내,
  [전용 푸기 복장](diva-special-pugi.md) 독립 저장·단장 권한·재접속 보존.
- [특별방 모험](diva-special-adventure.md): 실제 기간·정식 단원·획득 에리어를
  같은 트랜잭션에서 검사하고 1시간 귀환으로 등록. 일반 모험은 6시간을 유지합니다.
- [특별방 요리](diva-special-cooking.md): 클라이언트 자체 대성공 판정 영역 확대를
  확인했습니다. 서버는 결과를 그대로 보관하며, 정상 등록을 막던 SQL 타입 충돌을 수정했습니다.
- [붉은 보물](diva-map-special-elements.md): 획득 전 내용 숨김, 획득 후 공개.
  기존 v2에서는 `DivaMapRedTreasureMode` 설정을 유지합니다. 후속 승인된
  [v3 지도](diva-map-progressive-operation.md)는 10~65%의 단계별 대체 확률을
  자동 적용하고, 침공 주기·증가량·확률도 채웠습니다. 기존 회차는 보존합니다.
- [HR 선율](diva-hr-melody-restoration.md): GR 필요점수의 절반을 사용하는
  서버 커스텀 표입니다. 동일 계정 최고 10개와 기존 소비량 보존 규칙은 같습니다.
- [요격전 순위 보수 재조사](diva-interception-ranking-audit.md): 최종 공식
  자료에서 별도 순위별 지급표는 확인하지 못해 임의로 추가하지 않았습니다.

기존 마이그레이션 추적이 정상인 서버는 다음 시작 시 0059~0062를 적용합니다.
새 테이블/열을 사용하는 코드이므로 DB 마이그레이션을 생략하고 실행하면 안 됩니다.

## 테스트 범위

### 특별방 모험·요리·개인 누적 로그 후속 검증

- 가희 관련 전체 + 일반/특별 모험 회귀 `go test -race`: PASS, 243.738초.
  대상 정규식은 `^Test(Diva|RepoDiva|GuildAdventure|RegistGuildAdventure|LoadGuildAdventure|AcquireGuildAdventure|ChargeGuildAdventure)`입니다.
- 마지막 요리 보완 및 정상 모험 등록·다른 길드 변경 거절: PASS, 8.302초.
  실제 DB 정상 등록도 확인했으며 DB 접속 실패로 건너뛴 결과를 성공 근거로 삼지 않았습니다.
- `go test -race ./server/migrations -run 'Diva|TestMigrateEmpty|TestMigrateIdempotent'`: PASS, 11.598초.
- `go test -race ./config`: PASS, 2.581초.
- 최종 `go build ./...`, `go vet ./...`, 변경 Go 파일 포맷·`git diff --check`: PASS.

이 후속 작업은 새 마이그레이션을 추가하지 않습니다. 앞선 0059~0062를 포함한
현재 스키마를 localhost:5433 격리 DB에 적용해 검증했으며, 운영 DB에는 적용하지 않았습니다.

### 후속 HR 선율·v3 지도 검증

- `go test -race ./server/channelserver -run '^Test(Diva|RepoDiva)'`: PASS, 212.601초.
- 추가한 v3 과거 보물 영수증·페이지 전환 통합 테스트: PASS, 5.123초.
- `go test -race ./server/migrations -run 'Diva|TestMigrateEmpty|TestMigrateIdempotent'`: PASS, 11.034초.
  0062 최초 설치·재실행, 기존 v1/v2 JSON/원장 보존, v3 제약·침공 FK/중복 방지를 포함합니다.
- 전체 `go build ./...`, `go vet ./...`, `git diff --check`: PASS.

새 검증 역시 아래와 같은 격리 DB에서 순차 실행했습니다. 운영 DB나 실행 중인 게임에는
반영하지 않았으며, 보수 UI·붉은 아이콘·침공 로그의 실제 인게임 검증은 남아 있습니다.

### 선행 기능 검증 기록

Go 1.25.1, Windows, race detector, 격리 DB `127.0.0.1:5433/erupe_test`를
사용했습니다. DB 테스트는 패키지 사이에서도 동시에 실행하지 않았습니다.
빌드·정적 검사, 설정 테스트, 새 기능의 순수/핸들러/DB 테스트 및 신규
마이그레이션 보존 테스트를 수행했습니다.

- 최종 신규 기능·공지 `go test -race`: PASS, 22.092초.
- migrations 전체에서 아래 기존 이력 미추적 테스트 1개만 제외: PASS, 15.301초.
- config 전체 `go test -race`: PASS, 2.976초.
- 최종 `go build ./...`, `go vet ./...`, `git diff --check`: PASS.

확장 회귀 `Diva|Guild|Shop`에서 드러난 새 테스트 이름 길이와 영문 공지 폭
문제는 수정했습니다. 다음 기존 테스트 문제는 이번 기능과 분리했습니다.

- `TestMigrateExistingDBWithoutSchemaVersion`: 최신 전체 스키마를 만든 뒤
  schema_version만 삭제한 상태에서 0034를 재적용하여 raviente_runs 중복 실패.
  0034와 해당 테스트는 수정하지 않았습니다. 일반 신규/추적된 DB 업데이트
  경로와 별개이며, 이력 추적이 사라진 운영 DB에서 재시도를 권하지 않습니다.
- `TestGetCharacterMembershipApplicantKeepsApplicationGuild`
- `TestApplicationMutationsAreGuildScoped`
- `TestRemoveCharacterIsGuildScoped`
- `TestArrangeCharactersRejectsCrossGuildMemberAtomically`
- `TestSetRecruiterRejectsCrossGuildMember`
- `TestCancelInviteCannotCrossGuild`

위 수렵단 6개는 변경하지 않은 테스트 fixture의 캐릭터명이 DB의 varchar(15)
제한을 넘어서 실패합니다. 전체 저장소 테스트가 모두 통과했다고 주장하지 않습니다.

## 인게임 확인과 미복원 부분

전체 후속 테스트 동선은 [인게임 확인표](diva-player-validation.md)에 정리했습니다.

1. HR/GR 요격 점수 달성 후 선율 보수 수령, 잔액 조회, 1/2선율 교환을 확인합니다.
   계정의 다른 캐릭터로 바꾸어도 사용량이 초기화되지 않아야 합니다.
2. 실제 가영 기간에 1에리어를 확보한 수렵단의 특별방 입장과 전용 푸기
   복장 변경·재접속 유지·다른 단원에게 보이는 모습을 확인합니다.
3. 점수 보고와 시간 정산 뒤 로그가 나타나고, 수렵단 이동 후 이전 로그가
   남지 않는지 확인합니다.
4. v3 새 회차에서는 침공 전후 필요점수·전황 로그, 붉은 보물 내용 공개와 아이콘 전환을 확인합니다.
   원본 아이콘 두 종류와 색상 대응은 아직 실제 화면 검증이 필요합니다.

선율 HR 원본 획득 조건, 원본 특수 타일/배치 공식은 미확보이며 이번에는
승인받은 대체 수치로 채웠습니다. 요리의 특별방 우대는 덤프에서 확인했고,
모험 등록 권한은 후속 보완했습니다. 다만 무료 노래의 다중 클라이언트 효과,
동행자 출현·전투와 특별방 티켓 상점까지 실제 게임에서 확인한 것은 아닙니다.
알 수 없는 클라이언트 필드나 효과를 임의 값으로 활성화하지 않았습니다.
선율 구매 패킷은 비용만 전달하므로 클라이언트 아이템 지급과
서버 차감을 완전히 원자화할 수 없는 기존 프로토콜 한계도 남습니다.
