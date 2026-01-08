# Multinic Operator 보고서 (MGMT -> OpenStack -> Viola)

## 1. 목표와 범위

MGMT 클러스터의 OpenstackConfig CR을 기반으로 아래 작업을 수행하는 Go 기반 모놀리식 오퍼레이터:
1) Contrabass API에서 OpenStack 접속 정보를 수집
2) Keystone 토큰 발급 후 Neutron 포트 조회
3) VM ID별 네트워크 인터페이스 구성
4) Viola API로 JSON 전송(Agent용 CR 생성/갱신 목적)

오퍼레이터 내부에 Inventory API를 내장하고 파일 기반 DB(JSON)에 최신 상태를 저장.

## 2. 현재 구현 흐름

1) OpenstackConfig CR 이벤트 발생
2) Contrabass provider 조회
   - URL: `${CONTRABASS_ENDPOINT}/v1/contrabass/admin/infra/provider/{providerID}`
   - 인증 없음(현재 환경 기준)
3) adminPw/rabbitMQPw 복호화
   - AES-128-CBC, PKCS5 padding
   - Base64(IV + ciphertext)
   - 키: `CONTRABASS_ENCRYPT_KEY`
4) Keystone 토큰 발급 (서비스 카탈로그 포함)
   - POST `${OS_BASE_URL}/auth/tokens` (password grant)
5) Neutron 엔드포인트 결정
   - catalog(type=network)에서 interface/region 기준 선택
   - 필요 시 `OPENSTACK_NEUTRON_ENDPOINT`로 강제 지정
6) Neutron 포트 조회
   - GET `${NEUTRON_ENDPOINT}/v2.0/ports?project_id=...&device_id=...`
7) NodeConfig 변환
8) Viola API 전송
   - POST `${VIOLA_ENDPOINT}/v1/k8s/multinic/node-configs`
   - Body: NodeConfig 배열
   - Header: `x-provider-id` = openstackProviderID (옵션)

기본 requeue는 5분(폴링 fallback).

### 2.1 현재 진행 상태

- Contrabass/Keystone/Neutron 연동 및 Viola POST 구현 완료
- Inventory API + 파일 기반 DB(JSON) 업서트 및 hash 중복 방지 구현 완료

## 3. OpenstackConfig CRD 스펙

`vmNames`에는 OpenStack VM ID(서버 UUID)를 넣는 것으로 사용 중.

```
apiVersion: multinic.example.com/v1alpha1
kind: OpenstackConfig
metadata:
  name: openstackconfig-sample
  namespace: multinic-system
spec:
  subnetName: "test-sub"
  vmNames:
    - "measure-biz-worker-2"   # 실제로는 VM ID
  credentials:
    openstackProviderID: "66da2e07-a09d-4797-b9c6-75a2ff91381e"
    k8sProviderID: "optional"
    projectID: "df64928216f740d3a6b84a66fa30b649"
```

## 4. Viola 전송 Payload (현재)

POST `/v1/k8s/multinic/node-configs`

Body는 NodeConfig 배열:

```
[
  {
    "nodeName": "vm-uuid-1",
    "instanceId": "vm-uuid-1",
    "interfaces": [
      {
        "id": 1,
        "portId": "port-uuid",
        "macAddress": "fa:16:3e:aa:bb:cc",
        "address": "192.168.10.5",
        "cidr": "",
        "mtu": 0,
        "deviceId": "vm-uuid-1",
        "networkId": "net-uuid",
        "subnetId": "subnet-uuid"
      }
    ]
  }
]
```

CIDR/MTU는 추후 subnet/network 조회로 채움.

## 5. 오퍼레이터 환경 변수

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

운영에서는 encrypt key는 Secret 사용 권장.

## 6. 인벤토리 DB (파일 기반 JSON) 설계

오퍼레이터 내부에 Inventory API를 내장하고 JSON 파일에 최신 상태를 저장.
Viola API는 DB에 직접 접근하지 않고 Inventory API만 조회.
파일 기반 저장소이므로 오퍼레이터는 1개 replica 운영을 권장.

### 6.1 데이터 구조

- Record
  - providerId
  - nodeName
  - instanceId
  - config (NodeConfig 전체)
  - lastConfigHash
  - updatedAt (RFC3339)

### 6.2 Inventory API

- 목록 조회: `GET /v1/inventory/node-configs`
  - query: `providerId`, `nodeName`, `instanceId`
- 단건 조회: `GET /v1/inventory/node-configs/{nodeName}?providerId=...`

## 7. RabbitMQ 이벤트 + 폴링 안전망

빠른 반응은 RabbitMQ 이벤트 구독으로 처리, 장애/폭주 시 폴링으로 복구.

- consumer는 manual ack
- prefetch/QoS로 burst 제어
- DLQ + max-length + TTL로 폭주 방지
- reconnect + exponential backoff
- 이벤트 중복은 lastConfigHash로 idempotent 처리

## 8. 리스크 및 후속 작업

1) Neutron endpoint 선택 시 interface/region 값 운영 환경에 맞게 검증 필요
2) CIDR/MTU 조회: subnet/network 추가 호출
3) `vmNames` 필드명은 VM ID로 사용 중 (필요 시 `vmIDs`로 변경)
4) Contrabass 인증 필요 시 토큰 옵션 추가
5) 파일 기반 JSON 업서트 + lastConfigHash 중복 방지 적용

## 9. 코드 위치

- CRD: `api/v1alpha1/openstackconfig_types.go`
- Reconciler: `internal/controller/openstackconfig_controller.go`
- Contrabass client: `pkg/contrabass/client.go`
- AES 복호화: `pkg/crypto/aescbc.go`
- Keystone client: `pkg/openstack/keystone.go`
- Neutron client: `pkg/openstack/neutron.go`
- Viola client: `pkg/viola/client.go`

## 10. 배포/검증 기록 (2026-01-08)

### 10.1 배포 상태

- 이미지 태그: `expert.harbor.bf.okestro.cloud/library/multinic-operator:dev`
- 컨트롤러 롤아웃: 완료

### 10.2 확인 결과

- 컨트롤러 파드 정상 실행 확인
- 프로젝트 ID 갱신 후 Keystone scoped 토큰 발급 성공 확인
  - `projectID=0d5f63c52fc94aeeb767e69790fa73c8`
  - Contrabass 복호화된 `adminPw`는 `CloudExpert2025!`
- Viola 테스트 API 배포 후 POST 성공 확인
  - `viola-api` Service/Deployment (namespace: `multinic-system`)
  - 컨트롤러 로그: `synced node configs to viola` (count=3)
  - 테스트 API 로그에서 `x-provider-id` 헤더와 payload 수신 확인
  - 현재 `vmNames`가 실제 VM ID가 아니어서 `interfaces`가 null로 전송됨

### 10.3 확인 체크리스트 (환경 복구 후)

1) `kubectl -n multinic-operator-system get pods`
2) `kubectl -n multinic-operator-system logs deploy/multinic-operator-controller-manager --since=5m`
   - `synced node configs to viola` 로그 확인
3) `kubectl -n multinic-operator-system get svc multinic-operator-inventory-service`
4) `kubectl -n multinic-system get openstackconfigs.multinic.example.com -o yaml`
   - `projectID`, `vmNames` 값 확인

### 10.4 조치 필요사항

- `projectID`가 실제 접근 가능한 프로젝트인지 확인
  - scoped token 실패 시 `projectID` 변경 필요
- admin 비밀번호는 Contrabass 복호화 값(CloudExpert2025!)과 일치해야 함
- Viola API 엔드포인트 확인 및 `VIOLA_ENDPOINT` 환경 변수에 반영 필요
- 실제 포트 수집을 위해 `vmNames`에는 VM 이름이 아니라 VM ID(UUID)를 입력
