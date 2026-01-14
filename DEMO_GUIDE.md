# MultiNIC 데모 가이드 (new-biz-cluster)

## 0. 대상 환경

- MGMT 클러스터: 현재 kubeconfig 컨텍스트 (`kubernetes-admin@cluster.local`)
- Biz 클러스터: `root@192.168.3.170` (new-k8s-bastion)
- 데모 전제: Operator/Viola API가 MGMT 클러스터에 배포되어 있음

## 1. 사전 점검 (MGMT)

### 1-0) Operator 재배포(데모용)

```sh
# 기존 배포 삭제
helm uninstall multinic-operator -n multinic-operator-system

# 재배포
helm upgrade --install multinic-operator deployments/helm \
  -n multinic-operator-system --create-namespace \
  -f deployments/helm/values.yaml
```

### 1-1) Operator/Viola API 상태

```sh
kubectl -n multinic-operator-system get deploy multinic-operator-controller-manager
kubectl -n multinic-system get deploy viola-api
```

### 1-2) 필수 Secret 확인

```sh
kubectl -n multinic-system get secret contrabass-encrypt-key
# 테스트용 라우팅 사용 시
kubectl -n multinic-system get secret viola-api-routing
```

### 1-3) OpenstackConfig 유지 확인

```sh
kubectl -n multinic-system get openstackconfig
```

## 2. Biz 클러스터 초기화 (new-biz)

### 2-1) 기존 MultiNicNodeConfig 삭제

```sh
sshpass -p 'cloud1234' ssh -o StrictHostKeyChecking=no root@192.168.3.170 \
  "kubectl delete multinicnodeconfig -n multinic-system --all"
```

### 2-2) 삭제 확인

```sh
sshpass -p 'cloud1234' ssh -o StrictHostKeyChecking=no root@192.168.3.170 \
  "kubectl get multinicnodeconfig -n multinic-system"
```

## 3. 데모 실행 (OpenStack 포트 부착)

### 3-1) OpenStack에서 VM에 포트 부착

예시 (직접 OpenStack에서 수행):

```sh
openstack port create --network <network-name> <port-name>
openstack server add port <vm-id> <port-id>
```

또는 네트워크만 부착:

```sh
openstack server add network <vm-id> <network-name>
```

## 4. Operator 동작 확인 (MGMT)

### 4-1) OpenstackConfig 재동기화 (선택)

```sh
kubectl -n multinic-system annotate openstackconfig <name> \
  multinic.example.com/force-reconcile="$(date -u +%Y-%m-%dT%H:%M:%SZ)" --overwrite
```

### 4-2) Viola API 수신 로그 확인

```sh
kubectl -n multinic-system logs deploy/viola-api --tail=200
```

## 5. Biz 클러스터 적용 확인

### 5-1) MultiNicNodeConfig 생성 확인

```sh
sshpass -p 'cloud1234' ssh -o StrictHostKeyChecking=no root@192.168.3.170 \
  "kubectl get multinicnodeconfig -n multinic-system"
```

### 5-2) 상태 확인

```sh
sshpass -p 'cloud1234' ssh -o StrictHostKeyChecking=no root@192.168.3.170 \
  "kubectl describe multinicnodeconfig <nodeName> -n multinic-system"
```

## 6. 데모 종료(선택)

### 6-1) Biz 클러스터 정리

```sh
sshpass -p 'cloud1234' ssh -o StrictHostKeyChecking=no root@192.168.3.170 \
  "kubectl delete multinicnodeconfig -n multinic-system --all"
```

### 6-2) OpenStack 포트 제거 (선택)

```sh
openstack server remove port <vm-id> <port-id>
openstack port delete <port-id>
```

## 참고

- OpenstackConfig 필수값: subnetIDs/subnetID/subnetName, vmNames, openstackProviderID, k8sProviderID, projectID, contrabassEncryptKey, violaEndpoint
- Viola API POST는 `x-provider-id = k8sProviderID` 필수
- 노드당 인터페이스 최대 10개 (`multinic0~multinic9`)
- OpenstackConfig 생성 시각 이후 포트만 처리
