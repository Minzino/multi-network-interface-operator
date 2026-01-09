# multinic-operator

MGMT 클러스터에서 OpenstackConfig CR을 감시하고 OpenStack 네트워크 정보를 수집한 뒤
Viola API로 노드별 인터페이스 정보를 전송하는 오퍼레이터입니다.

## 개요

- 입력: OpenstackConfig CR (providerID, projectID, VM ID 목록)
- 처리: Contrabass → Keystone → Neutron 포트 조회
- 출력: Viola API로 JSON POST (MultiNicNodeConfig 생성용, subnetName 기준 필터)
- 저장: 오퍼레이터 내부 Inventory API + 파일 기반 DB(JSON)에 최신 상태 upsert (UI 조회용)

주의:
- `subnetName`은 네트워크명이 아니라 **서브넷 이름**입니다.
- `vmNames`에는 **VM ID(UUID)** 를 넣어야 합니다.

## 전제

- Go 1.25+
- Kubernetes 클러스터 접근 권한
- Contrabass/OpenStack API 접근 가능

## 환경 변수

```
CONTRABASS_ENDPOINT=https://expert.bf.okestro.cloud
CONTRABASS_ENCRYPT_KEY=conbaEncrypt2025
CONTRABASS_TIMEOUT=30s
CONTRABASS_INSECURE_TLS=true

OPENSTACK_TIMEOUT=30s
OPENSTACK_INSECURE_TLS=true
OPENSTACK_NEUTRON_ENDPOINT=
OPENSTACK_ENDPOINT_INTERFACE=public
OPENSTACK_ENDPOINT_REGION=

VIOLA_ENDPOINT=http://viola-api.multinic-system.svc.cluster.local
VIOLA_TIMEOUT=30s
VIOLA_INSECURE_TLS=false

INVENTORY_ENABLED=true
INVENTORY_ADDR=:18081
INVENTORY_DB_PATH=/var/lib/multinic-operator/inventory.json
```

## 동작 흐름

1) OpenstackConfig CR 이벤트 발생
2) Contrabass provider 조회 및 adminPw 복호화
3) Keystone 토큰 발급 (서비스 카탈로그 포함)
4) Neutron 엔드포인트 결정 (카탈로그 또는 환경 변수)
5) subnetName → subnet/network 조회 (CIDR/MTU 확보)
6) Neutron 포트 조회 (device_id == VM ID)
7) 대상 subnet에 포함된 포트만 선별
8) 노드별 인터페이스 구성
9) Viola API POST
10) 파일 기반 DB(JSON) 최신 상태 upsert (providerId + nodeName 기준)

## Inventory API (오퍼레이터 내장)

- 목록 조회: `GET /v1/inventory/node-configs`
  - query: `providerId`, `nodeName`, `instanceId`
- 단건 조회: `GET /v1/inventory/node-configs/{nodeName}?providerId=...`

Kubernetes Service: `inventory-service` (port 18081)

주의: 파일 기반 저장소이므로 오퍼레이터는 1개 replica로 운영하는 것을 권장합니다.
지속 저장이 필요하면 `config/manager/manager.yaml`의 `emptyDir`를 PVC로 교체하십시오.

### Inventory API 확인 예시

```sh
kubectl -n multinic-operator-system port-forward svc/inventory-service 18081:18081
curl -s "http://127.0.0.1:18081/v1/inventory/node-configs?providerId=<provider-id>"
curl -s "http://127.0.0.1:18081/v1/inventory/node-configs/<nodeName>?providerId=<provider-id>"
```

## 설치/배포 (기본)

```sh
make install
make deploy IMG=<registry>/multinic-operator:tag
```

샘플 CR 적용:

```sh
kubectl apply -k config/samples/
```

## 테스트용 Viola API

Viola 개발 API가 준비되기 전까지 아래 테스트용 API를 배포해 POST 수신 여부를 확인할 수 있습니다.

```sh
kubectl apply -f config/test/viola-test-api.yaml
```

## 테스트 메모

- Multinic Agent는 `NODE_NAME` 기준으로 `MultiNicNodeConfig/{nodeName}`를 조회하므로
  `metadata.name`을 실제 노드명과 동일하게 맞춰야 합니다.

## 문서

- 설계/현황: `REPORT.md`
- 작업 계획: `PLAN.md`

## 라이선스

Apache-2.0
