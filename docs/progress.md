# 진행 상황

[implementation-plan.md](implementation-plan.md)의 단계별 현황.

> 범례: ⬜ 대기 · 🟨 진행중 · ✅ 완료

| 단계 | 상태 | 패키지 |
|---|---|---|
| P0 에러 분류 | ✅ | `patch/errors.go` |
| P1 빌더 | ✅ | `patch/build.go`, `buildkey.go`, `buildvalue.go` |
| P2 구조 검증 | ✅ | `patch/validate.go` |
| P3 값 모델 | ✅ | `patch/value.go` |
| P4 주소 해석 | ✅ | `patch/resolve.go`, `patchproto/cont.go` |
| P5 적용 | 🟨 | `patchproto/` (적합성 코퍼스 남음) |
| P6 RFC 6902 변환 | ⬜ | `jsonpatch/` |

---

## 계획에서 벗어난 결정

### 패키지 분할: `internal/spec` → `patch`

계획서는 공유 규칙을 `internal/spec`에 두기로 했으나 **`internal/`은 사용자가 import할 수 없다.** 에러 분류가 곧 API라고 정해놓고(계획 §2.1) 그것을 `internal/`에 두면 사용자가 `Code`를 검사할 수 없다.

분할 기준을 **"메시지 인스턴스를 만지는가"**로 바꿨다:

| 패키지 | 다루는 것 |
|---|---|
| `patch/` | `Patch` 문서 + 메시지 **descriptor**. 인스턴스 없이 판정 가능한 전부 |
| `patchproto/` | `protoreflect.Message` 인스턴스 |
| `patchwire/` (나중) | `[]byte` |

`Range` 정규화는 길이만 주면 순수 함수이고, `Field` 제약 해석과 `Value` arm 적법성은 descriptor만 있으면 된다 — `patchwire`도 wire format을 의미 있게 걷으려면 descriptor가 필요하므로 셋 다 `patch/`에 속한다. 공유 규칙이 하나의 구현을 갖는다는 목적은 그대로 달성되고, 에러 타입은 공개된다.

---

## P0 — 에러 분류 ✅

`patch/errors.go`

스키마의 FAILURE CONTRACT 조항을 **25개 `Code`**로 옮겼다. 문서 구조(대상 불필요) / descriptor 결합 / 인스턴스 결합 셋으로 묶여 있고, 각 `Code`의 주석이 어느 조항인지 밝힌다.

- **`At`** — 문서 내 위치를 Go 셀렉터 문법으로 표기 (`delta.entries[2].targets.selectors[0]`). 조항이 스물 몇 개인 형식에서 "적용 실패"만 반환하면 디버깅이 불가능하므로 선택이 아니다.
- **`CodeOf(err)`** — 래핑된 에러 체인에서 `Code`를 꺼낸다. sentinel 25개를 두는 것보다 낫다.
- `errors.Is(err, &patch.Error{Code: ...})`도 동작한다.

`TestCodeHasName`이 모든 `Code`에 이름이 있고 중복이 없음을 고정한다. `Code`를 추가하면서 목록을 갱신하지 않으면 실패한다 — 의도한 바다.

### 설계 노트

**`CodeUnknownField`가 forward-compat의 실질적 집행자다.** protobuf는 미지의 oneof arm을 "미설정"으로 보고하므로, `WhichKind()`만으로는 *"프로듀서가 안 넣었다"*와 *"내가 모르는 arm이다"*를 구별할 수 없다. unknown field 집합이 유일한 구별 수단이고, 따라서 P2의 순회가 스키마에서 가장 중요한 fail-closed 조항을 집행한다.

**`CodeFieldConflict`는 `CodeVacantTarget`과 별개다.** `Field`의 식별자들이 서로 다른 필드를 가리키면 그건 "없다"가 아니라 "이 Patch는 다른 스키마를 대상으로 쓰였다"는 뜻이다. vacancy는 `on_missing`으로 넘어갈 수 있지만 이것은 절대 안 된다.

---

## P1 — 빌더 ✅

`patch/build.go` · `buildkey.go` · `buildvalue.go`

```go
p, err := patch.New("example.v1.User",
    patch.Target(patch.Name("profile")).Assign(patch.Str("hi")),
    patch.Container().In(patch.Name("tags")).Assign(patch.List(patch.Str("a"))),
    patch.Target(patch.Span(-2, -1)).Skip().Remove(),
)
```

### 무효한 Patch를 타입으로 막는다

계획서에 적은 *"빌더로 만들 수 없는 것이 있어야 한다"*를 메서드 집합으로 구현했다. 아래는 **컴파일되지 않는다**:

| 못 만드는 것 | 어떻게 막았나 | 대응 조항 |
|---|---|---|
| 빈 `Targets` | `Target(first Selectorer, rest ...Selectorer)` — 첫 인자가 별도 | `CodeEmptyCollection` |
| 빈 `Delta` | `New(mt, first Op, rest ...Op)`, `Nest(first, rest...)` | `CodeEmptyCollection` |
| scope 없는 엔트리 | `Target()` / `Container()`가 유일한 시작점 | `CodeMissingOneof` |
| kind 없는 엔트리 | 종결 메서드만 `Op`를 반환 | `CodeMissingOneof` |
| `on_missing`을 켠 `test` | `Skip()`이 `Test`/`Exists`가 없는 `TolerantScope`를 반환 | `CodeTestNotStrict` |
| 컨테이너로의 `move`/`copy` | `ContainerScope`에 `Move`/`Copy` 메서드가 없음 | `CodeIllegalScope` |

타입으로 못 막는 나머지 — `Append`를 `remove`/`assign`/`test`/`nest`에 준 경우, 빈 이름, 필드 번호 0, arm 없는 `Value` — 는 빌더가 에러를 누적해 `New`에서 반환한다.

### 설계 노트

**`Keyer`가 `Selectorer`를 포함한다.** 스키마의 `Selector`는 정확히 `Key` + 다중값 arm이므로, 한 위치를 지목하는 것은 곧 "0개 이상"도 지목한다. 역은 성립하지 않고, 그래서 `Path`는 `Keyer`만 받는다 — 경로는 정확히 하나의 컨테이너에 닿아야 하기 때문이다. 타입 관계가 스키마의 주장과 일치한다.

**`Field`는 제약이지 선택자가 아니다.** `patch.Name("s_1").Num(109)`는 두 식별자를 **모두** 실어 보낸다. 적용 시점에 하나로 해석하고 나머지로 검증하므로, 필드가 재번호된 스키마에 옛 Patch를 적용하면 조용히 성공하는 대신 `CodeFieldConflict`로 거부된다.

**`Span` 계열이 presence를 표현한다.** `SpanAll()` / `SpanFrom(0)` / `SpanTo(0)`이 서로 다른 세 개의 와이어 상태다. 구 구현이 `end <= 0`으로 분기해 명시적 `[0,0)`을 "전체"로 읽던 버그가 재발할 수 없다 — 애초에 값 0과 미설정을 다른 생성자로 나눴다.

`TestSpanPresence`가 이 셋의 `HasBegin()`/`HasEnd()` 조합을 고정한다.

---

## P2 — 구조 검증 ✅

`patch/validate.go` — `func Validate(p *patchpb.Patch) error`

대상 메시지 없이 판정 가능한 규칙 전부. 첫 위반을 반환한다.

### unknown field 순회가 이 단계의 전부다

protobuf는 **미지의 oneof arm을 "미설정"으로 보고한다.** 따라서 `WhichKind()`만으로는 *"프로듀서가 안 넣었다"*와 *"내가 모르는 arm이다"*를 구별할 수 없고, **unknown field 집합이 유일한 구별 수단이다.**

`Validate`가 다른 무엇보다 먼저 `findUnknown`을 돌리는 이유가 이것이다 — 이게 통과하기 전에는 문서에 대해 관측한 어떤 것도 보이는 대로의 의미라고 믿을 수 없다.

가장 위험한 사례를 테스트로 고정했다:

```go
// Value는 14-15를 미래 arm으로 예약해두었다.
// v2가 그중 하나를 쓴 Value를 v1이 읽으면 WhichKind()는 not_set을 반환한다.
v := &patchpb.Value{}
setUnknown(v, 14)
// → CodeUnknownField. "값이 없다"로도, 하물며 "clear"로도 읽히지 않는다.
```

스키마가 미설정 `Value.kind`를 오류로 규정한 것이 바로 이 경로를 막기 위해서였고, 검증이 그것을 집행한다. `TestValidateRefusesUnknownFields`가 Patch 최상위 / 중첩 Entry / 빈 payload 메시지(`Remove{}`) / 3단 중첩 Delta 깊은 곳 / repeated 원소 / **와이어 왕복 후** 여섯 위치를 모두 확인한다.

### 검증 항목

| 대상 | 확인 |
|---|---|
| 문서 | unknown field, `message_type`, `min_reader_revision <= Revision`, `delta` 존재·비어있지 않음 |
| 엔트리 | `scope` 설정, `kind` 설정, `targets.selectors` 비어있지 않음, `on_missing` 인식 가능, 컨테이너로의 `move`/`copy` 금지 |
| `test` | `want` 설정, `on_missing` 미설정 |
| 셀렉터 | arm 설정, `append`는 `insert`/`move`/`copy`에서만 |
| 키 | arm 설정, `Field`에 식별자 존재(빈 이름·번호 0 거부), `MapKey` arm 설정 |
| 값 | `kind` 설정, `MessageValue`/`ListValue`/`MapValue` 재귀, `FieldValue`/`MapEntry`의 키·값 필수 |
| 위치 | `origin` 설정, `key` 설정 |
| 중첩 | `Nest.delta` 재귀 |

에러의 `At`이 문서 내 경로를 정확히 짚는다:

```
delta.entries[0].nest.delta.entries[0].nest.delta.entries[0].assign.value
```

### 테스트 구성

`TestValidateEntry`의 케이스는 **전부 `patchpb`로 직접 조립한다.** 빌더로는 만들 수 없는 상태들이기 때문이다(P1 표 참조) — 그리고 그게 바로 검증이 필요한 모집단이다. 반대로 `TestValidateAcceptsWhatTheBuilderProduces`는 빌더 산출물이 오탐되지 않음을 확인한다.

---

## P3 — 값 모델 ✅

`patch/value.go`

```go
func ShapeOf(v *patchpb.Value) Shape
func CheckArm(v *patchpb.Value, fd protoreflect.FieldDescriptor, site Site, at At) error
func Scalar(v *patchpb.Value, fd protoreflect.FieldDescriptor, site Site, at At) (protoreflect.Value, error)
func MapKeyFor(k *patchpb.MapKey, fd protoreflect.FieldDescriptor, at At) (protoreflect.MapKey, error)
```

`value.proto`의 arm 표를 코드로 옮겼다. **변환 격자가 없다** — 맞지 않는 arm은 넓히거나 좁히거나 자르지 않고 `CodeIllegalArm`이다. 구 구현의 `patchproto/cast.go`에 해당하는 것이 아예 존재하지 않는다.

### `Site` — 같은 필드가 위치에 따라 다른 arm을 받는다

`repeated string` 필드는 **전체로는** `l`을, **원소 하나로는** `s`를 받는다. 이 구분이 없으면 `CheckArm`이 총함수가 될 수 없다.

| Site | 대상 |
|---|---|
| `SiteField` | 필드 자체. cardinality에 따라 `l` / `map` / 스칼라·`m` |
| `SiteElement` | repeated 필드의 원소 하나 |
| `SiteMapValue` | map 필드의 값 하나 |

`TestArmIsOneToOne`이 16개 필드 × 10개 값 생성자 조합을 전수로 돌려, 맞는 arm 하나만 통과하고 나머지는 전부 `CodeIllegalArm`임을 고정한다.

### closed enum — 런타임 제약을 우회해야 했다

스키마는 *"CLOSED enum은 선언된 번호만 받는다"*고 규정한다. 이를 테스트하려면 진짜 closed enum이 필요한데, **protobuf-go v1.36.11이 enum 자신의 `options.features`를 파싱하지 않는다.** `Enum.unmarshalSeed`(`internal/filedesc/desc_init.go`)는 부모에서 상속만 하고, File·Message·Field·Extension과 달리 자기 옵션은 읽지 않는다. 그래서 enum 본문에 `option features.enum_type = CLOSED;`를 써도 descriptor에는 들어가지만 `IsClosed()`는 계속 false다.

→ `proto/sample/closed.proto`를 만들어 **파일 수준**으로 선언했다. 이유는 그 파일 주석에 기록해뒀다.

### 맵 키

arm이 선언된 키 타입과 정확히 일치해야 한다 — 숫자 키가 문자열 맵에 문자열화되지 않는다(구 구현은 `fmt.Sprintf("%d", ...)`로 강제 변환했다). arm이 맞아도 **값이 범위를 벗어나면 오류**다: `map<int32,V>`에 `i: 2^31`은 절단도 아니고 `on_missing`이 건너뛸 수 있는 vacancy도 아니다.

`m_i32_s` `m_i64_s` `m_u32_s` `m_u64_s` `m_si64_s` `m_ux32_s` `m_b_s`를 sample에 추가했다 — 구 저장소가 커버리지 공백으로 인정했던(`root-replace-plan.md:84`) 정수·bool 키 맵이다.

---

## P4 — 주소 해석 🟨

`patch/resolve.go` — descriptor만으로 되는 부분. 인스턴스가 필요한 경로 탐색·셀렉터 확장·중복 검출은 P5에서 `patchproto/`에 들어간다.

```go
func ResolveField(md protoreflect.MessageDescriptor, f *patchpb.Field, at At) (protoreflect.FieldDescriptor, bool, error)
func NormalizeIndex(i int64, length int) (int, bool)
func NormalizeRange(r *patchpb.Range, length int) (int, int)
```

### vacancy가 세 번째 반환값인 이유

`ResolveField`와 `NormalizeIndex`가 `(값, vacant bool, err)` 형태다. **vacancy를 `error`에 접으면 `test.exists = false`를 구현할 수 없다** — 부재를 읽어야 하는데 부재가 곧 실패가 되어버리기 때문이다. 스키마 초안이 정확히 이 함정에 빠졌고(redesign §5), 그래서 타입으로 분리했다.

호출부에서 세 위치가 각각 다르게 처리한다:

| 위치 | vacancy 처리 |
|---|---|
| `Entry.targets` + `test` | **읽는다** — `exists`가 보고하는 것이 이것 |
| `Entry.targets` + 나머지 | `on_missing` 적용 (기본: 실패) |
| `Path`, `Location` | 무조건 오류 |

### 충돌은 vacancy가 아니다

`Field`의 식별자들이 서로 다른 필드를 가리키면 `CodeFieldConflict`이고, **`on_missing`으로 건너뛸 수 없다.** 그건 "없다"가 아니라 "이 Patch는 다른 스키마를 대상으로 쓰였다"는 뜻이고, 이 형식이 가진 유일한 무결성 검사이기 때문이다.

`TestResolveFieldConflict`가 다섯 가지 불일치를 확인하며, 그중 어느 것도 vacant로 보고되지 않음을 명시적으로 검사한다.

해석 순서는 **번호 → 이름 → json_name**이다. 필드 번호가 protobuf의 안정된 정체성이고 이름은 와이어 파손 없이 바뀔 수 있기 때문이다. 나머지 설정된 식별자는 전부 해석 결과와 대조된다.

### Range는 스키마 주석이 곧 테스트다

`TestNormalizeRangeMatchesTheSchema`가 `path.proto`의 워크드 예제 6개를 그대로 돌린다. 명세가 예제이므로 예제가 테스트다.

`HasBegin()`/`HasEnd()`로 분기한다 — 구 구현은 `end <= 0`으로 분기해 **명시적 `[0,0)`이 리스트 전체를 선택했다.** `TestNormalizeRangeReadsPresence`가 `[0,0)` / `[0,_)` / `[_,0)` 셋을 구별함을 고정한다. 그리고 `TestNormalizeRangeNeverWraps`가 구 스키마 주석이 주장하던 `[-2,2)` = "마지막 둘 + 처음 둘"의 wrap-around가 없음을 확인한다.

---

## P5 — 적용 🟨

`patchproto/apply.go` · `cont.go` · `container.go` · `value.go`

```go
func Apply[T proto.Message](m T, p *patchpb.Patch, opts ...Option) (T, error)
```

7 kind × 4 scope가 모두 동작한다. 적합성 코퍼스(`conformance/`)는 아직이다.

### 원자성 — in-place API가 없다

`Apply`는 사본에 적용하고 성공했을 때만 반환한다. `TestApplyIsAtomic`이 세 가지를 고정한다: 뒤 엔트리가 실패하면 앞 엔트리의 변경이 남지 않는다, 입력이 제자리에서 변하지 않는다, `test`만 담은 Patch는 아무것도 바꾸지 않는다.

`wantErr` 헬퍼가 **모든 실패 케이스마다** 입력 무손상을 확인한다 — 원자성이 특정 테스트가 아니라 모든 오류 경로의 불변식이다.

### 구현이 스키마 모순을 찾아냈다

`assign`/map key 표는 *"없으면 생성한다"*였는데 vacancy 규칙은 *"없는 맵 키는 vacant이고 기본은 실패"*였다. **둘 다 참일 수 없다** — 그러면 맵 항목을 만들 방법이 없다. `TestMapOps/assign_creates`가 이걸 잡았다.

원인은 서로 다른 두 상황을 한 단어로 부른 것이다:

| | 뜻 | 처리 |
|---|---|---|
| **NO SLOT** | 주소가 아무 위치도 가리키지 않음 — 선언되지 않은 필드, 범위 밖 인덱스 | 모든 연산에 대해 missing target |
| **EMPTY SLOT** | 위치는 있는데 내용이 없음 — 항목 없는 맵 키 | `remove`·`nest`에만 missing. 쓰기 연산은 채운다 |
| **PRESENCE** | 선언된 필드가 설정되었는가 | missing이 아님. `exists`가 보고하는 것 |

선언된 메시지 필드는 미설정이어도 **유효한 위치**다. 그래서 `remove`가 동작하고 `exists=false`가 성립한다. 스키마에 이 구분을 반영하고 `vacant`라는 용어를 `missing target`으로 통일했다 (`.proto`에 `vacant` 표현이 0개 남았다).

### 그 밖에 구현이 강제한 것

- **`probe`는 presence를 실시간으로 읽는다.** 해석 시점에 굳혀두면 `test exists`가 틀린 답을 낸다 — 범위 밖 인덱스의 `NormalizeIndex`가 `idx=0`을 돌려주므로 `noSlot` 표시 없이는 0번 원소를 검사하게 된다.
- **`move`/`copy`는 메시지 값을 복제한다.** 아니면 소스를 지울 때 목적지까지 비워진다. `TestMoveAndCopy/copying_a_message_does_not_alias_it`가 고정한다.
- **컨테이너 `assign`은 staged 메시지에 먼저 채운다.** 선언되지 않은 필드를 가리키는 값이 오면 **비우기 전에** 실패해야 한다. 구 구현은 먼저 지우고 나서 해석 못한 키를 버려서 부분 데이터 손실을 만들었다.
- **`remove`는 인덱스 내림차순으로 지운다.** pre-entry 인덱스를 유지하기 위해서다.
- **`spliceInto`는 리스트를 한 번에 재구성한다.** targets `[0, 2]`가 `[a b c]`에서 `[Z a b Z c]`가 되도록 — 첫 삽입이 둘째를 밀지 않는다.
