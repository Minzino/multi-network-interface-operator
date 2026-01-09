현재 오픈스택을 기반으로 만든 솔루션을 통해서 유저가 CR을 생성할건데 거기에는 아래와 같은 정보가 들어간다.

apiVersion: multinic.example.com/v1alpha1
kind: OpenstackConfig
metadata:
  name: openstackconfig-sample-contrabass-1
  namespace: multinic-system
  # Controller 레벨 설정 사용 (권장)
  # annotations는 필요 없음 - Controller에서 자동으로 ConfigMap 참조
spec:
  # 필수 필드들
  subnetID: "subnet-uuid"  # OpenStack 서브넷 ID (권장)
  subnetName: "test-sub"  # OpenStack 서브넷 이름 (subnetID 없을 때 사용)
  vmNames:
    - "measure-biz-worker-2"
    - "measure-biz-worker-3"
    - "measure-biz-master-1"

  # OpenStack 인증 정보 (Contrabass API 기반)
  credentials:
    # Contrabass API 기반 Provider 정보 (필수)
    openstackProviderID: "a919ea1c-9a09-4a9f-9021-ec68c1de2159"  # Contrabass API에서 조회할 Provider ID
    k8sProviderID: "f5861c22-b252-42b5-a0c5-cfb1d245c819"  # Kubernetes Provider ID (선택사항)
    projectID: "df64928216f740d3a6b84a66fa30b649"  # OpenStack Project ID (필수)
    
    # 기존 방식은 사용하지 않음 (Contrabass API 사용)
    # dataPlatformDB, username, password, domainName, providerID 등은 deprecated


그 이후 오픈스택 기반으로 만들어진 솔루션인 contrabass api에 요청을 통해서 오픈스택의 접속정보를 얻습니다.

# Contrabass API 호출
GET /api/providers/{openstackProviderID}

# 응답 예시
{
  "data": {
    "url": "http://keystone.example.com:5000/v3",
    "attributes": {
      "adminId": "admin",
      "adminPw": "U2FsdGVkX1+...", // AES-128 암호화된 비밀번호 (Base64 인코딩)
      "domain": "default"
    }
  }
}


그 다음에는 저희가 생각한 기능을 위해서 포트랑 vm 정보를 알아야하니 키스톤과 뉴트론 노바 등등을 활용해서 정보를 얻고 이게 DB가 필요할지 잘 모르겠는데요. 필요하다면 ERD 설계가 필요할거같습니다.

nodeName은 Nova 서버 조회 결과를 기준으로 결정합니다.
- metadata key 설정 시: 해당 metadata 값 우선
- 미설정 시: server name 사용
- 둘 다 없으면 vmID 사용


multinic agent용 api

MultiNic 관리 API



작성자 김윤석

6

반응 추가
Endpoint
 /v1/k8s/multinic/node-configs

Method
POST 

### Request Parameters
분류 | key | type | required | description
Request Heaader | x-provider-id | string | O | 비즈 클러스터 공급자 ID
 

Request Body
생성할 MultiNicNodeConfig 정보

예시)


[
  {
    "nodeName": "worker-1",
    "instanceId": "i-0123456789abcdef0",
    "interfaces": [
      {
        "id": 1,
        "macAddress": "00:1A:2B:3C:4D:5E",
        "address": "192.168.1.100",
        "cidr": "192.168.1.0/24",
        "mtu": 1500
      }
    ]
  }
]



## 26.01.08일 목요일
openstack client가 필요할 듯 함. 

현재 오퍼레이터 RabbitMQ 연동 미구현 상태
