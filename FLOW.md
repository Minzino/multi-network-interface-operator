# MultiNIC 전체 플로우

## 시퀀스 흐름

```mermaid
sequenceDiagram
  autonumber
  participant OS as OpenStack
  participant OP as Operator(MGMT)
  participant CB as Contrabass API
  participant KS as Keystone
  participant NE as Neutron
  participant NO as Nova
  participant VA as Viola API(MGMT)
  participant K8S as Biz K8s API
  participant AG as Agent(Job)

  OS->>OS: VM에 포트 생성/부착
  OP->>CB: Provider 조회 (openstackProviderID)
  CB-->>OP: Keystone URL + Admin 계정
  OP->>KS: 토큰 발급 요청 (projectID)
  KS-->>OP: 토큰 + 서비스 카탈로그
  OP->>NE: 포트 조회 (device_id=VM ID)
  OP->>NO: nodeName 조회 (metadata key > name > VM ID)
  OP->>OP: 서브넷/상태/기준시각 필터 적용
  OP->>OP: 노드별 인터페이스 매핑 (최대 10개)
  OP->>VA: POST /v1/k8s/multinic/node-configs (x-provider-id=k8sProviderID)
  VA->>K8S: MultiNicNodeConfig CR 적용
  K8S->>AG: Agent Job 스케줄링
  AG->>AG: 인터페이스 이름/MTU/IP/라우트 적용
  AG->>AG: OS별 영속 설정 파일 작성
  AG-->>K8S: CR Status 업데이트
```

## 흐름도 (Flowchart)

```mermaid
graph TD
  A[OpenStack: VM 포트 생성/부착] --> B[Operator: OpenstackConfig 감시]
  B --> C[Contrabass Provider 조회]
  C --> D[Keystone 토큰 발급]
  D --> E[Neutron 포트 조회]
  E --> F[Nova nodeName 결정]
  F --> G[서브넷/상태/기준시각 필터]
  G --> H[노드별 인터페이스 매핑 10개 제한]
  H --> I[Viola API POST x-provider-id=k8sProviderID]
  I --> J[Biz K8s: MultiNicNodeConfig 적용]
  J --> K[Agent Job 실행]
  K --> L[인터페이스 적용 + 영속 설정]
  L --> M[CR Status 업데이트]
```

## 아키텍처

```mermaid
graph LR
  U[User/Operator CR 적용]

  subgraph MGMT["MGMT Cluster"]
    OP[Multinic Operator]
    VA[Viola API]
    INV[Inventory API]
    CB[Contrabass API]
  end

  subgraph OS["OpenStack"]
    KS[Keystone]
    NE[Neutron]
    NO[Nova]
  end

  subgraph BIZ["Biz Cluster"]
    KAPI[K8s API Server]
    CR[MultiNicNodeConfig CR]
    AG[Agent Job]
  end

  U -->|0 OpenstackConfig CR 생성/수정| OP
  OP -->|1 Provider 조회| CB
  OP -->|2 Token 요청| KS
  OP -->|3 Port 조회| NE
  OP -->|4 NodeName 조회| NO
  OP -->|5 노드별 인터페이스 POST| VA
  VA -->|6 CR 적용| KAPI
  KAPI -->|7 CR 생성/갱신| CR
  CR -->|8 Job 생성| AG
  OP -->|9 상태 저장| INV
```

## 단계별 상세 설명

1) Provider 조회  
   - Contrabass API로 `openstackProviderID` 기반 **대상 OpenStack 접속 정보를 조회**  
   - 결과: Keystone URL, Admin ID, 암호화된 Admin PW, 도메인, Nova/Neutron 관련 URL 정보

2) Token 요청  
   - Keystone에 `projectID`로 토큰 요청  
   - 결과: Token + Service Catalog 획득

3) Port 조회  
   - Neutron에서 `device_id == VM ID` 조건으로 포트 조회  
   - `subnetIDs/subnetID/subnetName` 필터로 대상 서브넷만 선별  
   - `openstackPortAllowedStatuses`에 포함된 상태만 처리

4) NodeName 조회  
   - Nova에서 VM 정보를 조회해 노드명 결정  
   - `settings.openstackNodeNameMetadataKey` 값이 있으면 metadata 우선, 없으면 서버 이름 사용

5) 노드별 인터페이스 매핑  
   - VM별 포트를 묶어 `NodeConfig` 구성  
   - 노드당 최대 10개(`multinic0~9`) 제한 적용

6) Viola API POST  
   - `violaEndpoint`로 인터페이스 목록 전송  
   - 헤더 `x-provider-id = k8sProviderID` 사용

7) CR 적용  
   - Viola API가 Biz 클러스터에 `MultiNicNodeConfig` 적용 요청

8) Job 생성/실행  
   - Biz 클러스터 Agent가 노드별 Job 실행  
   - 인터페이스 이름/MTU/IP/라우트 적용 및 영속 파일 작성

9) 상태 저장  
   - Operator가 Inventory에 최신 스냅샷 저장  
   - 조회 API에서 k8sProviderID 기준으로 검색 가능
