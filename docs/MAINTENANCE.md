# Multinic Operator 코드 유지보수 가이드

이 문서는 운영이 아니라 **코드 유지보수 관점**에서 폴더/파일 역할과 수정 포인트를 정리합니다.

## 1. 디렉터리 구조

- `api/`
  - CRD 타입 정의(`v1alpha1`)와 코드 생성 대상.
- `cmd/`
  - `main.go`: 오퍼레이터 엔트리포인트.
  - `viola-test-api/main.go`: 테스트용 Viola API 서버.
- `config/`
  - Kustomize 매니페스트, RBAC, CRD 정의.
  - `config/test/`: 테스트용 Viola API 매니페스트/라우팅 샘플.
- `deployments/helm/`
  - Helm 차트와 CRD 복사본(`crds/`).
- `internal/controller/`
  - OpenstackConfig 컨트롤러 핵심 로직.
- `internal/inventory/`
  - Inventory API 서버(`server.go`) + 파일 저장소(`store.go`).
- `pkg/`
  - `contrabass/`: Contrabass API 클라이언트.
  - `openstack/`: Keystone/Nova/Neutron 클라이언트.
  - `viola/`: Viola API 요청 모델/클라이언트.
  - `crypto/`: Contrabass 암호화 복호화.
- `images/`
  - 오프라인 배포용 이미지 tar.
- `test/`
  - 테스트 유틸/샘플.

## 2. 핵심 파일/흐름

- `internal/controller/openstackconfig_controller.go`
  - CR을 읽고, Contrabass → OpenStack → Viola 흐름을 수행.
  - 포트 필터링/중복 전송 방지/폴링 재시도 로직 포함.
- `pkg/contrabass/client.go`
  - provider 조회 및 adminPw 복호화.
- `pkg/openstack/*.go`
  - Keystone 토큰 발급, Neutron/Nova 호출.
- `pkg/viola/client.go`
  - 노드별 인터페이스 JSON POST.
- `internal/inventory/store.go`
  - 파일 기반 최신 상태 upsert.

## 3. 자주 수정되는 포인트

- CR 스키마 변경
  1) `api/v1alpha1/openstackconfig_types.go` 수정
  2) `make manifests`
  3) `config/crd/bases/...` → `deployments/helm/crds/...` 복사

- 설정 추가
  - `OpenstackConfigSettings` 필드 추가
  - `resolveSettings()` 매핑 추가
  - README/샘플 CR 업데이트

- Viola API 스펙 변경
  - `pkg/viola` 모델/클라이언트 수정
  - `openstackconfig_controller.go`의 payload 조립 로직 수정
  - 테스트용 라우팅: `cmd/viola-test-api/main.go`, `config/test/viola-test-api.yaml`

- Inventory 저장 방식 변경
  - `internal/inventory/store.go` (파일 포맷/키)
  - `internal/inventory/server.go` (조회 API)

## 4. 빌드/테스트

```sh
go test ./...
make manifests
```

## 5. 배포 관련 파일(코드 관점)

- Kustomize: `config/` 하위
- Helm: `deployments/helm/`
- 오프라인 이미지: `images/`

## 6. 인계 포인트

- 핵심 로직: `internal/controller/openstackconfig_controller.go`
- CRD 정의: `api/v1alpha1/openstackconfig_types.go`
- 클라이언트: `pkg/contrabass/`, `pkg/openstack/`, `pkg/viola/`
