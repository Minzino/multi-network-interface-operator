# Multinic Operator 작업 계획 (사전 작성)

## 1. 목표

- MGMT 클러스터의 OpenstackConfig CR을 기반으로 OpenStack 포트 정보를 수집
- Viola API로 노드별 인터페이스 정보를 POST 전송
- 최신 인터페이스 상태를 파일 기반 DB(JSON)에 upsert (Multus UI 조회용)
- 중복 전송/중복 저장 방지 (해시 기반)

## 2. 진행 단계

1) **중복 방지 로직 추가**
   - 포트/인터페이스 리스트 정규화
   - 노드별 `lastConfigHash` 계산
   - 이전 해시와 동일하면 Viola POST 및 파일 기반 DB upsert 스킵

2) **파일 기반 DB(JSON) 업서트 통합 (Inventory API 내장)**
   - 환경 변수로 파일 경로 주입
   - `providerId + nodeName` 기준 upsert
   - 필드: config_json, instanceId, updatedAt, lastConfigHash

3) **Viola 전송 헤더 확정**
   - `x-provider-id` 헤더 전송 옵션 추가
   - 값은 `credentials.openstackProviderID`

4) **문서 갱신**
   - 오퍼레이터 흐름, 중복 방지, 파일 기반 저장 구조

5) **테스트**
   - 해시 계산/정규화 단위 테스트
   - 파일 기반 업서트/저장 로직 단위 테스트 (mock or integration 옵션)

## 3. 필요 입력값

- `INVENTORY_ADDR` (예: `:18081`)
- `INVENTORY_DB_PATH` (예: `/var/lib/multinic-operator/inventory.json`)

## 4. 완료 기준

- 동일 데이터는 전송/저장되지 않음
- 새로운 포트 추가/삭제 시 Viola POST + 파일 기반 DB upsert 실행
- 문서(한글) 최신화
- 파일 기반 저장소 특성상 오퍼레이터 replica는 1개로 유지
