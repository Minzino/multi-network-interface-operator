# Multinic Operator 유지보수 가이드

## 1. 목적

- MGMT 클러스터에서 OpenstackConfig CR을 감시해 OpenStack 포트 정보를 수집합니다.
- 수집된 정보를 Viola API로 전송해 Biz 클러스터에 MultiNicNodeConfig를 생성/갱신합니다.
- 최신 상태를 파일 기반 Inventory(DB)에 저장해 조회 API를 제공합니다.

## 2. 구성 요소

- Controller: `internal/controller/openstackconfig_controller.go`
- CRD: `OpenstackConfig` (`config/crd/bases/...`)
- Inventory API: `internal/inventory` + `/v1/inventory/*`
- 테스트용 Viola API: `cmd/viola-test-api` + `config/test/viola-test-api.yaml`
- Helm 차트: `deployments/helm`

## 3. 입력값(필수/권장)

필수:
- `spec.credentials.openstackProviderID`
- `spec.credentials.projectID`
- `spec.vmNames` (VM ID UUID 목록)
- `spec.subnetID` 또는 `spec.subnetName`
- Contrabass 암호화 키
  - 기본: `<namespace>/contrabass-encrypt-key` Secret의 `CONTRABASS_ENCRYPT_KEY`
  - 또는 `spec.secrets.contrabassEncryptKeySecretRef`
  - 또는 `spec.settings.contrabassEncryptKey`

권장:
- `spec.settings.contrabassEndpoint`
- `spec.settings.violaEndpoint`
- `spec.settings.openstackPortAllowedStatuses` (기본: ACTIVE,DOWN)
- `spec.settings.pollFastInterval`/`pollSlowInterval`

Secret 생성 예시:

```sh
kubectl -n multinic-system create secret generic contrabass-encrypt-key \
  --from-literal=CONTRABASS_ENCRYPT_KEY=conbaEncrypt2025
```

## 4. 동작 흐름(요약)

1) Contrabass API로 Provider 조회
2) 복호화된 admin 계정으로 Keystone 토큰 발급
3) Neutron에서 포트 조회 (device_id == VM ID)
4) Nova에서 nodeName 결정
5) Viola API로 노드별 인터페이스 전송
6) Inventory 파일 DB에 최신 상태 upsert

## 5. 배포/업데이트 절차

### 5.1 이미지 빌드 및 저장

```sh
nerdctl build -t nexus.okestro-k8s.com:50000/multinic-operator:<tag> .
nerdctl save -o images/multinic-operator_<tag>.tar nexus.okestro-k8s.com:50000/multinic-operator:<tag>
```

### 5.2 Helm 배포

```sh
helm upgrade --install multinic-operator deployments/helm \
  -n multinic-operator-system --create-namespace \
  --set image.repository=nexus.okestro-k8s.com:50000/multinic-operator \
  --set image.tag=<tag> \
  --set image.pullSecrets[0].name=nexus-regcred
```

### 5.3 개발용(kustomize) 배포

```sh
make deploy IMG=nexus.okestro-k8s.com:50000/multinic-operator:<tag>
```

### 5.4 CRD 변경 시

- `make manifests` 실행 후
- `config/crd/bases/...`를 `deployments/helm/crds/...`에 복사

## 6. 운영 체크리스트

- OpenstackConfig에 `settings.contrabassEndpoint`, `settings.violaEndpoint`가 설정되어 있는지 확인
- Contrabass 암호화 키 Secret 존재 여부 확인
- Neutron/Nova 엔드포인트가 catalog에서 잘 해석되는지 확인
- Viola API 서비스가 정상 동작하는지 확인
- Inventory 저장 경로가 쓰기 가능한지 확인

## 7. 장애 대응

- `spec.settings.contrabassEndpoint` 미설정 오류
  - CR에 contrabassEndpoint 추가
- TLS 인증서 오류 (`x509`)
  - `spec.settings.contrabassInsecureTLS=true` 설정
- Neutron 엔드포인트 미검출
  - catalog 확인 또는 `spec.settings.openstackNeutronEndpoint` 설정
- 포트가 DOWN 상태로만 수집됨
  - `openstackPortAllowedStatuses` 조정 또는 포트 상태 확인
- 노드 인터페이스가 비어 있음
  - 해당 노드의 포트/서브넷 매핑 재확인

## 8. 로그 확인

```sh
kubectl logs -n multinic-operator-system deployment/multinic-operator-controller-manager
```

주요 키워드:
- `synced node configs to viola`
- `no changes detected`
- `ContrabassError`, `KeystoneError`, `NeutronPortError`

## 9. Inventory API

- 목록: `GET /v1/inventory/node-configs`
- 단건: `GET /v1/inventory/node-configs/{nodeName}?providerId=...`
- 파일 기반 저장이므로 **replica 1개**를 권장합니다.

## 10. 인계 포인트

- 핵심 로직: `internal/controller/openstackconfig_controller.go`
- CRD 스키마: `config/crd/bases/multinic.example.com_openstackconfigs.yaml`
- Helm 차트: `deployments/helm`
