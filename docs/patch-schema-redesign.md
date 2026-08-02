# Patch 스키마 재정의 기록

`proto/patch/{patch,path,value}.proto` — 정의 결함 S1–S21을 해소하기 위한 전면 재작성의 결정 기록.

> 배경: [patch-spec-defects.md](patch-spec-defects.md) (S1–S21) · [patch-schema-review.md](patch-schema-review.md) (구현 괴리 포함 전체 검토)

---

## 1. 설계 원칙

재정의 전체를 관통하는 다섯 가지. 개별 결정이 갈릴 때는 이 순서로 판단했다.

1. **Fail closed.** 리더가 이해하지 못하거나 해석하지 못하는 것은 **문서 전체를 중단시키는 오류**다. 유일한 예외(`Entry.on_missing`)는 **와이어에 기록되는 작성자의 선택**이며, 리더의 기본값이 되는 일은 없다.
2. **자기서술적 값.** protobuf 타입 부류마다 `Value` arm 하나. 변환 격자 없음. 대상 descriptor 없이도 `Value`를 검증할 수 있다.
3. **부재는 의미가 아니다.** 컨테이너 스코프는 명시적 oneof arm이지 빈 타겟 목록이 아니다. `null`은 완전히 삭제했다.
4. **단일값/다중값은 타입으로 구분.** `Key`(정확히 한 위치, `Path`에 사용) vs `Selector`(0개 이상, `targets`에 사용). S7의 비대칭이 이제 **구조적으로 자명**하다.
5. **`.proto`가 명세다.** 모든 규칙이 주석에 있다. README는 파생 문서다.

---

## 2. 구조 변화

| 구 스키마 | 신 스키마 | 이유 |
|---|---|---|
| `Delta` (문서 겸 조각) | `Patch` (문서) + `Delta` (조각) | `Delta`는 컨테이너 상대적이라 단독 해석 불가 — 이제 그 사실이 타입에 있다 (S18) |
| `Segment` (name/index/field/range) | `Key` (field/index/map_key) + `Selector` (key/range/append) | 단일값과 다중값 분리 (S3, S7) |
| `FieldSegment` (name/name_alt/number) | `Field` (name/json_name/number, 제약) + `MapKey` (s/i/u/b) | 네 네임스페이스 분리 (S2, S3, S13) |
| `Struct` (메시지 겸 맵) | `MessageValue` + `MapValue` | 자기서술 (S8) |
| `Value` (3개 숫자 캐리어 + null) | `Value` (타입별 arm, null 없음) | 무변환·정규 인코딩 (S9, S10) |
| `bool remove` | `message Remove {}` | 모든 kind가 수식자를 실을 수 있게 (S21) |
| `targets` 비어있음 = root | `oneof scope { Targets, Container }` | 부재를 의미로 쓰지 않음 (S6) |
| (없음) | `Patch.message_type`, `min_reader_revision` | 스키마 식별·진화 (S17, S18) |
| (없음) | `Entry.on_missing` | 관용의 유일한 통로, 기본값 실패 (S1) |
| `move`/`copy` 소스 = `FieldSegment` | `Location { oneof origin; Key }` | 컨테이너 간 이동 표현 (S16) |

---

## 3. S1–S21 해소

| # | 해소 | 핵심 텍스트 |
|---|---|---|
| S1 실패 계약 | ✅ | `patch.proto`의 FAILURE CONTRACT — 원자성, fail-closed 21개 조항, 관용의 유일 통로 |
| S2 선택자 vs 제약 | ✅ | `Field`를 **제약**으로 확정. 해석 순서(number→name→json_name) + 나머지 검증, 불일치는 오류 |
| S3 number 네 네임스페이스 | ✅ | `Key.kind` = field/index/map_key, `MapKey` = s/i/u/b. 음수 인덱스는 모든 연산에서 동일 |
| S4 RangeSegment | ✅ | 3단계 정규화 규칙 하나. 모순된 예시 삭제, wrap-around 폐기 |
| S5 targets 컬렉션 의미 | ✅ | 순서 없는 집합, 중복은 오류, **엔트리 시작 전 상태** 기준 |
| S6 root = 부재 | ✅ | `oneof scope`. 빈 `Targets`는 오류 |
| S7 path/targets 비대칭 | ✅ | `Key`(단일값) / `Selector`(다중값) 타입 분리로 구조적 자명화 |
| S8 Value 자기서술 | ✅ | `MessageValue` / `ListValue` / `MapValue` 별도 arm |
| S9 숫자 도메인 | ✅ | 타입 부류별 arm(b/i32/i64/u32/u64/f32/f64/s/x/e). enum은 `sint32 e` 전용 |
| S10 NullValue 중복 | ✅ | null arm 삭제. clear는 `remove`, 부재 단언은 `test.exists=false` |
| S11 Struct 중복 키 | ✅ | 유일성 필수, 중복은 오류. 대상 oneof 멤버 둘도 오류 |
| S12 null 원소 | ✅ | null이 없으므로 표현 불가. 인코딩 길이 = 디코딩 길이 |
| S13 KeyValue.key 타입 | ✅ | `MapKey`가 bool·전체 uint64 범위를 표현. 범위 초과는 오류 |
| S14 연산 의미 | ✅ | 7 kind × 4 스코프 전부 기술 (`move`/`copy` 포함) |
| S15 test:null | ✅ | `Test.want` = value \| exists. **vacancy** 개념으로 리스트·맵에서도 성립 |
| S16 move/copy 소스 | ✅ | `Location`. `append`도 허용해 RFC 6902 전역성 회복 |
| S17 미지 arm | ✅ | 미지 arm **및 미지 필드 번호** 전부 오류. `enum_type = OPEN` 고정 |
| S18 스키마 식별자 | ✅ | `Patch.message_type` 필수, `min_reader_revision` |
| S19 presence 의존 | ✅ | 세 파일 모두 `option features.field_presence = EXPLICIT` 명시 |
| S20 패키지 버저닝 | ⚠️ 변형 | 접미사 대신 **이름 자체로 버저닝** — 다음은 `patchv2` (아래 참조) |
| S21 bool remove | ✅ | `message Remove {}` |

---

## 4. 선택지가 있던 곳에서 내린 결정

### 4.1 패키지 버저닝 — 접미사 대신 이름

`patch.v1`이 아니라 **`patch`**. 다음 파괴적 개정은 `patchv2` (`proto/patchv2/`).

`buf lint`의 `PACKAGE_VERSION_SUFFIX`는 `buf.yaml`에서 근거와 함께 예외 처리했다. 같은 `patch` 안에서의 **호환 가능한** 진화는 `Patch.min_reader_revision`이 관리한다.

> 이 결정 때문에 구 스키마와 FQN이 충돌하므로 구 `.proto` 3개는 제거했다 (§6).

### 4.2 `required_features` 문자열 → `min_reader_revision` 정수

문자열 레지스트리는 **유지 규율이 필요**하고, 스키마가 그 규율을 강제할 수단이 없다. 어떤 문자열이 존재하는지 `.proto`가 말하지 않으면 두 개의 적법한 v1 리더가 같은 문서에 대해 갈린다.

단조 증가 정수는 레지스트리도, 명명 규약도, 중복/정렬 규칙도 필요 없다. **대가**는 조밀도다 — 개정이 일어나면 바뀐 구조를 쓰지 않는 문서까지 구버전 리더가 거부한다. 형식이 아직 젊으므로 이 교환은 옳다고 판단했다. 주석에 그 대가를 명시했다.

### 4.3 canonical 주장 — 축소

초안은 *"두 Delta는 의미가 같을 때에만 같다"*고 주장했다. **거짓이다.** `MessageValue.fields`·`MapValue.entries`·`Targets.selectors`가 전부 순서 무관 컬렉션인데 순서 있는 `repeated`에 저장된다.

정렬 요구로 "벌어들이는" 것도 가능했지만, canonical form은 **요구된 적 없는 속성**이고 프로듀서에게 검증하기 어려운 부담을 지운다. 주장을 정직하게 축소하고, `Patch`에 proto 동등성을 쓰지 말라고 명시했다.

다만 부수적으로 `ON_MISSING_FAIL = 1`은 삭제했다 — `UNSPECIFIED = 0`과 같은 뜻의 두 번째 인코딩이었고, 이는 S10에서 고친 바로 그 병이다.

### 4.4 `append` + `move`/`copy` — 허용

초안은 `append`를 `insert` 전용으로 제한했다. 그러면 `{"op":"copy","from":"/a/0","path":"/b/-"}`가 **표현 불가능**해지고 RFC 6902 변환이 전역이 아니게 된다. 제한에 근거가 없었으므로 `insert`/`move`/`copy` 셋 다 허용한다. `remove`/`assign`/`test`/`nest`에는 여전히 오류다 — 이들은 리스트를 늘릴 수 없다.

### 4.5 원자성 — "사전 검증 또는 스냅샷"에서 "스냅샷만"

초안은 두 방법을 동등하게 제시했다. **동등하지 않다.** 엔트리의 적법성은 앞선 엔트리가 만든 상태에 의존할 수 있으므로 전체 사전 검증은 원리적으로 불가능하다. 명세를 **사본에 적용 후 성공 시 공개**로 좁히고, 호출자 소유 메시지를 제자리 변경하는 API는 이 계약을 지킬 수 없으므로 주 인터페이스로 제공해서는 안 된다고 명시했다.

---

## 5. 2차 검증에서 잡힌 것

초안을 6개 관점으로 다시 공격해 36건 확인 / 32건 기각. 전부 반영했다. 가장 중요한 것:

**vacancy 개념 누락 (근본 원인).** 초안에는 *"주소는 이 컨테이너에 유효한데 거기 아무것도 없다"*를 가리키는 말이 없었다. 그 상태를 "해석되지 않음"으로 뭉갰고, fail-closed 목록이 그것을 무조건 중단으로 만들었다. 결과:

- S15를 위해 추가한 `test.exists = false`가 **리스트 인덱스와 맵 키에서 결코 성립할 수 없었다.** 키가 없으면 → 해석 실패 → 중단. 키가 있으면 → 단언 실패 → 중단.
- 유일한 탈출구 `ON_MISSING_SKIP`은 같은 문단이 불가능하다고 선언한 **공허한 통과**를 만들어냈다.
- `Key`의 포괄 규칙이 `remove` 표의 인접한 두 행(메시지 필드 / 맵 키)과 서로 모순됐다.

**vacant location**을 `Key`에 한 번 정의하고 모든 규칙이 이를 인용하게 해서 셋 다 해소했다. `test`는 vacancy를 **읽고**, 다른 kind는 `on_missing`의 지배를 받으며, `Path`와 `Location`에서는 무조건 오류다.

나머지 반영 사항:

| 지적 | 조치 |
|---|---|
| `move`/`copy`가 28칸 중 6칸 공백 | 전체 표 작성. 타겟 쓰기는 **assign 의미**, 소스 clear는 **소스 컨테이너의 remove 규칙** |
| 빈 `Path`가 두 의미 (root vs 현재 컨테이너) | `Path`는 **항상 둘러싼 `Delta`의 컨테이너 기준**. `Location`은 `oneof origin` |
| fail-closed가 oneof arm만 커버 | **미지의 필드 번호**도 오류. `enum_type = OPEN` 고정 + 미선언 `OnMissing` 값도 오류 |
| `Test.want`에 필수 규칙 없음 | 필수 명시 + 미지 arm 목록·필수 oneof 목록에 추가 + `reserved 3 to 15` |
| 대상 메시지의 oneof 미언급 | 한 oneof의 멤버 둘은 오류. `insert`의 "이미 설정됨"에 oneof 형제 포함 |
| `MapKey.i`/`u`가 32비트 키에 64비트 캐리어 | 선언된 키 타입의 범위를 벗어나면 오류 (절단 아님, vacancy 아님) |
| `Value.e`가 closed enum에 무제한 | CLOSED enum은 선언된 번호만, OPEN은 임의 int32 |
| `test.exists = true`가 컨테이너에서 미정의 | true = 비어있지 않음 |
| enum 번호가 무관한 enum으로 이식 가능 | `move`/`copy`는 kind + **선언된 타입**까지 일치 요구 |
| 대상의 unknown field 처리 미정의 | **보존 필수** — 문서가 지목하지 않은 데이터를 버리지 않는다 |

기각된 32건은 대부분 protobuf 자체가 이미 정의한 것(메시지 동등성 등)을 스키마가 재정의해야 한다는 요구이거나, 오늘 재현되지 않는 추측성 미래 대비였다.

---

## 6. 현재 상태와 남은 일

**완료**

- `proto/patch/{patch,path,value}.proto` — `package patch;`, `buf build`·`buf lint` 클린
- `patchpb/` 생성 완료, `go build ./patchpb/` 통과
- 구 `proto/patch/{delta,segment}.proto` 제거, `value.proto`는 교체
- `buf.yaml`에 `PACKAGE_VERSION_SUFFIX` 예외 + 근거

**남은 일 — 구현 전면 재작성**

구 `dpb/`와 세 백엔드(`patchproto/`, `patchjson/`, `patchstruct/`)는 구 스키마를 대상으로 쓰였고 **소스 `.proto`를 잃었다.** 지금은 `go build ./...`가 통과하지만 이는 아무도 둘을 함께 import하지 않기 때문이며, 함께 링크하면 init에서 죽는다:

```
panic: proto: file "patch/path.proto" has a name conflict over patch.Path
        previously from: ".../dpb"
        currently from:  ".../patchpb"
```

즉 `dpb/`는 **제거 대상**이지 공존 대상이 아니다. 구현을 새로 쓸 때 참고할 것:

- `patchproto/cast.go` — 없어진다. `Value`가 자기서술적이므로 변환 격자가 필요 없다.
- `patchproto/diff.go` — `Diff`는 `Patch`를 발행하도록 다시 쓴다. `message_type`을 채워야 한다.
- `patchjson/`, `patchstruct/` — descriptor 없는 백엔드가 `Value.m`/`l`/`map`을 **디코딩할 수 없다면 오류를 반환**해야 한다 (구 구현은 `nil`을 반환해 assign을 파괴적 null 쓰기로 만들었다).
- 원자성 계약상 `Patch(v proto.Message, ...)` 형태의 제자리 변경 API는 주 인터페이스가 될 수 없다. `Patched`(clone 후 적용)가 기본이어야 한다.
- `jsonpatch/` + `FromJsonPatch` — `append`가 `move`/`copy`에도 허용되므로 이제 RFC 6902에 대해 전역이 될 수 있다. 단 RFC의 `add`(기존 멤버 덮어쓰기)는 `insert`가 아니라 `assign`으로 가야 한다.
- `proto/sample/value.proto` — `package patch.sample`이라 `patch`와 한 네임스페이스를 공유한다. 테스트 전용이므로 별도 패키지로 옮기는 편이 낫다.
