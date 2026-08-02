# 구현 계획

`proto/patch/*.proto`를 구현하기 위한 계획. 스키마는 확정되었고 이 문서는 그것을 코드로 옮기는 범위·구조·순서를 다룬다.

> 스키마의 근거: [patch-schema-redesign.md](patch-schema-redesign.md) · [patch-spec-defects.md](patch-spec-defects.md) · [patch-schema-review.md](patch-schema-review.md)
>
> 구 구현 참조본: `/workspaces/github.com/lesomnus/protobuf-diff` (커밋 `2a96ef1`)

---

## 1. 범위

### 1.1 만드는 것

| | 내용 |
|---|---|
| **검증** | `Patch`가 스키마를 만족하는지 판정. 대상 없이 가능한 것과 대상이 필요한 것으로 나뉜다 |
| **적용** | `proto.Message`에 `Patch`를 적용. 원자적 |
| **빌더** | `Patch`를 손으로 조립하기 위한 계층 |
| **RFC 6902 변환** | JSON Patch 문서 → `Patch` |

### 1.2 만들지 않는 것

| | 이유 |
|---|---|
| **`Diff`** | 범위에서 제외. 두 메시지로부터 `Patch`를 생성하지 않는다 |
| **JSON 백엔드** | `protojson.Unmarshal` → `Apply` → `protojson.Marshal`로 충분하다. 새 스키마는 `Field.number` 검증·`MapKey`의 선언된 키 타입 일치·`Value.e`의 CLOSED enum 검사에 descriptor가 필요하므로, descriptor를 요구하는 순간 별도 백엔드가 버는 게 없다 |
| **Go struct 백엔드** | 위와 같음 |
| **`patchwire`** | 나중에. §5 참조 |

**`Patch`를 만드는 경로는 빌더와 RFC 6902 변환 둘뿐이다.** `Diff`가 없으므로 빌더의 사용성이 곧 라이브러리의 사용성이다.

---

## 2. 새 스키마가 강제하는 설계

구 구현을 옮길 수 없는 이유. **이 넷이 아키텍처를 결정한다.**

### 2.1 에러가 곧 API다

fail-closed 조항이 21개다. 구 구현은 이해할 수 없는 것을 만나면 `return nil`이었고, 그래서 "적용됨"과 "아무것도 안 함"을 호출자가 구별할 수 없었다. 새 스키마에서는 **각 조항이 구별 가능한 에러여야 한다** — 그렇지 않으면 fail-closed가 "무언가 실패했다"는 뭉뚱그린 신호로 퇴화한다.

따라서 에러 분류가 **제일 먼저** 나온다. 다른 모든 코드가 그것을 반환한다.

### 2.2 원자성이 in-place API를 금지한다

> An applier that mutates a caller-owned message in place cannot honor this contract and MUST NOT be offered as the primary interface. — `patch.proto`

엔트리의 적법성이 앞선 엔트리가 만든 상태에 의존할 수 있으므로 **전체 사전 검증은 원리적으로 불가능**하다. 사본에 적용하고 성공 시 교체하는 방법뿐이다.

```go
func Apply[T proto.Message](m T, p *patchpb.Patch, opts ...Option) (T, error)
```

구 구현의 `Patch(m, delta) error`는 **존재할 수 없다.**

### 2.3 vacancy는 에러가 아니라 타입이다

`test.exists = false`가 성립하려면 *"주소는 유효한데 거기 아무것도 없다"*를 **읽을 수 있어야** 한다. 해석기가 vacancy를 `error`로 반환하면 그 연산은 구현 불가능하다. (스키마 초안이 정확히 이 함정에 빠졌다 — [redesign §5](patch-schema-redesign.md) 참조.)

```go
type Resolution struct {
    Loc   Location // Found일 때만 유효
    Found bool     // false = vacant
}
```

해석 자체의 실패(arm이 컨테이너에 안 맞음, `Field` 식별자 불일치)는 `error`다. vacancy는 아니다.

### 2.4 해석과 적용이 분리되어야 한다

두 조항이 강제한다:

- *"All selectors resolve against the container state as it was BEFORE the entry began"* → 해석하며 적용하면 인덱스가 밀린다
- *"Two selectors in one entry that resolve to the same location are an error"* → 중복 검출은 **모든** 셀렉터를 해석한 뒤에만 가능하다

구 구현은 `expandListTargets` → 즉시 적용이었다. 새 구현은 `resolve(all) → check duplicates → apply`다.

---

## 3. 패키지 구조

구 구현의 가장 큰 실패는 **세 백엔드가 같은 구조를 각자 해석해 서로 갈라진 것**이었다(원 검토 §3.5: "하나의 구조에 여러 해석기 — 전부 이미 갈라졌다"). 백엔드가 지금은 하나지만 `patchwire`가 예정되어 있으므로 구조로 막는다.

```
patchpb/            생성 코드 ✓ 완료
internal/spec/      스키마 규칙의 단일 구현 — 모든 소비자가 공유
  errors.go           21개 조항의 에러 분류
  validate.go         구조 검증 (대상 불필요)
  field.go            Field 제약 해석
  mapkey.go           MapKey arm 판정 + 범위 검사
  selector.go         Range 정규화, Append 적법성, 중복 검출
patch/              빌더 — Patch를 손으로 조립
patchproto/         proto.Message 백엔드
  resolve.go          Path/Key 탐색 (spec 사용)
  value.go            Value ↔ protoreflect.Value
  apply.go            7 kind × 4 scope
jsonpatch/          RFC 6902 문서 파싱 ✓ 유지
  convert.go          Doc → patchpb.Patch (신규)
conformance/        적합성 코퍼스 (신규)
internal/sample/    테스트 픽스처 ✓ 유지
internal/x/         테스트 헬퍼 ✓ 유지
```

**규칙: `internal/spec`에 있는 판단을 소비자가 재구현하지 않는다.** `Range` 정규화가 두 곳에 있으면 두 곳이 갈라진다. 구 구현에서 정확히 그 일이 일어났다(`expandListTargets` vs `RangeSegment.Match`가 presence·부호·합집합 셋 다 불일치).

---

## 4. 단계

의존 순이다. 각 단계는 그 아래 단계 없이 테스트 가능해야 한다.

### P0 — 에러 분류 (선행)

`patch.proto`의 FAIL CLOSED 목록 21개 조항을 코드로 옮긴다. 각 에러는 **어느 조항을 위반했는지**와 **문서 내 어디인지**를 말해야 한다.

```go
type Code int

const (
    CodeMessageTypeMismatch Code = iota + 1
    CodeReaderRevisionTooOld
    CodeUnrecognizedArm
    CodeUnknownField
    CodeUnrecognizedOnMissing
    CodeMissingOneof
    CodeMissingField
    CodeEmptyCollection
    CodeDuplicateTarget
    CodeFieldIdentifierConflict  // 식별자 불일치 — vacancy와 구별된다
    CodeIllegalArmForContainer
    CodeMapKeyOutOfRange
    CodeUndeclaredEnumValue
    CodePathNotReached
    CodeTestFailed
    CodeTestVacuous
    CodeSourceUnresolved
    CodeTypeMismatch
    CodeVacantTarget             // on_missing이 기본값일 때
    // ...
)

type Error struct {
    Code Code
    At   string // "delta.entries[2].targets.selectors[0]"
    ...
}
```

21개 조항이 있는 형식에서 "적용 실패"만 반환하면 디버깅이 불가능하다. **문서 내 경로는 선택이 아니다.**

**완료 기준**: 조항 ↔ 코드 매핑이 테스트로 고정된다.

### P1 — 빌더

`Diff`가 없으므로 손 조립이 주된 생성 경로다. 그리고 **P2 이후의 모든 테스트가 이걸로 `Patch`를 만든다** — 조기에 필요한 이유다.

```go
p := patch.New("example.v1.User",
    patch.At(patch.Field("profile")).Assign(patch.Str("hello")),
    patch.AtContainer(patch.Field("tags")).Insert(patch.List(patch.Str("a"))),
)
```

설계 원칙: **빌더로 만들 수 없는 것이 있어야 한다.** 구조적으로 무효한 `Patch`(미설정 필수 oneof, 빈 `Targets`, `test`에 `on_missing`)는 타입으로 막는다. fail-closed 형식에서 런타임 오류로 미룰 이유가 없다.

> 무효 케이스 테스트는 빌더를 우회해 `patchpb`를 직접 써야 한다. 그게 정상이다 — 빌더가 무효한 걸 만들 수 있으면 빌더가 실패한 것이다.

**완료 기준**: 스키마의 모든 연산·셀렉터·값 종류가 빌더로 표현 가능하다.

### P2 — 구조 검증 (대상 불필요)

```go
func Validate(p *patchpb.Patch) error
```

대상 메시지 없이 판정 가능한 것 전부:

- **미지의 필드 번호** — `Patch` 트리 전체를 재귀 순회하며 `m.ProtoReflect().GetUnknown()`이 비어있는지 확인. 중첩 `Delta` 안까지. **이 단계에서 가장 손이 많이 가는 부분이다.**
- 미설정 필수 oneof: `Entry.scope`, `Entry.kind`, `Key.kind`, `MapKey.kind`, `Selector.kind`, `Value.kind`, `Test.want`, `Location.origin`
- 미설정 필수 필드: `FieldValue.key/value`, `MapEntry.key/value`, `Location.key`, `Patch.delta`
- 빈 컬렉션: `Targets.selectors`, `Delta.entries`
- 미선언 `OnMissing` 값 (`enum_type = OPEN`이라 관측 가능)
- `Field`에 식별자 0개, 또는 빈 `name`/`json_name`
- `test` 엔트리에 `on_missing`이 설정됨
- `append`가 `remove`/`assign`/`test`/`nest`와 함께 쓰임

> **미지의 arm은 protobuf 런타임에서 미설정과 같은 관측이 된다.** `WhichKind()`가 not-set을 반환할 때 그것이 "안 넣었다"인지 "내가 모르는 arm"인지는 **unknown field 집합으로만 구별된다.** 즉 위 순회가 forward-compat 규칙의 실질적 집행자다. 이걸 빼면 스키마의 fail-closed 조항 중 가장 중요한 것이 집행되지 않는다.

**완료 기준**: 각 항목마다 거부 테스트 + **정상 `Patch`가 오탐되지 않는다**는 테스트.

### P3 — 값 모델

```go
func ToProto(v *patchpb.Value, fd protoreflect.FieldDescriptor) (protoreflect.Value, error)
```

- `Value` arm ↔ proto kind **정확 일치**. 변환 없음 → 구 `patchproto/cast.go`는 **불필요하다** (`move`/`copy`도 정확 일치를 요구하므로)
- enum: CLOSED면 선언된 번호만, OPEN이면 임의 int32
- `MessageValue`/`ListValue`/`MapValue` ↔ 메시지/리스트/맵
- `MapKey` 범위 검사 (`i` against `map<int32,V>` → `[-2^31, 2^31)`)
- `MessageValue`의 대상 oneof 멤버 중복 검출

**완료 기준**: 13개 arm × 유효/무효 대상 조합 표. 경계값(±2^31, ±2^63, ±Inf, NaN) 테스트.

### P4 — 주소 해석

```go
func ResolveKey(c Container, k *patchpb.Key) (Resolution, error)   // vacancy는 error 아님
func ResolvePath(root Container, p *patchpb.Path) (Container, error) // vacancy = error
func ExpandSelectors(c Container, ts *patchpb.Targets) ([]Resolution, error)
```

- `Field` 제약: `number` → `name` → `json_name` 순 해석 후 **나머지 설정된 식별자 검증**. 불일치는 `CodeFieldIdentifierConflict` (vacancy 아님)
- `json_name` 검증은 `fd.JSONName()`과 비교
- `Range` 정규화 3단계. **`HasBegin()`/`HasEnd()`로 분기** — 값 0으로 분기하면 구 구현의 버그가 그대로 재발한다
- `Append`는 리스트 + `insert`/`move`/`copy`에서만
- 중복 검출은 해석된 위치 집합에서

**완료 기준**: vacancy가 세 위치(`Selector`/`Path`/`Location`)에서 각각 다르게 처리된다는 테스트. 스키마 주석의 `Range` 예시 6개가 그대로 통과.

### P5 — 적용

```go
func Apply[T proto.Message](m T, p *patchpb.Patch, opts ...Option) (T, error)
```

- **clone → 적용 → 성공 시 반환.** 실패 시 원본 무손상
- 대상의 unknown field는 **보존**. 컨테이너 스코프 `remove`/`assign`이 버리면 안 된다
- 엔트리마다: `path` 탐색 → 셀렉터 해석(pre-entry 상태) → 중복 검출 → 적용
- 7 kind × 4 scope 표를 `patch.proto` 주석 그대로

**포팅할 알고리즘**: 다중 타겟 `insert`의 splice. 참조본 `patchproto/list.go:29-63`에 pre-entry 인덱스로 삽입점을 모아 정렬 후 한 번에 재구성하는 코드가 있다. 새 스키마에서도 유효하고, `-1` 특수 처리가 `Append`로 분리되어 **더 단순해진다.**

**완료 기준**: 28칸 전부에 최소 하나의 테스트. `move`의 퇴화 사례(자기 자신으로 이동 → no-op, 소스 부재 → 오류).

### P6 — RFC 6902 변환

```go
func FromJsonPatch(doc jsonpatch.Doc, messageType string) (*patchpb.Patch, error)
```

`append`가 `move`/`copy`에도 허용되므로 **이제 전역이 될 수 있다.** 구 구현이 거부하던 `{"op":"copy","from":"/a/0","path":"/b/-"}`가 표현 가능하다.

매핑 변경에 주의:

| RFC 6902 | 구 구현 | 신 구현 |
|---|---|---|
| `add` (객체 멤버, 기존 존재) | `assign` | `assign` — **`insert`가 아니다.** 새 `insert`는 기존 값이 있으면 오류 |
| `add` (배열 인덱스) | `insert` | `insert` |
| `add` (`-` 토큰) | `insert` at `-1` | `insert` at `Selector.append` |
| `test` | **조용히 버림** | `test`로 변환 |
| cross-container `move`/`copy` | 오류 | `Location`으로 표현 |
| JSON `null` | `ValNull()` | **`remove`** (null arm 없음) |

`message_type`이 필수이므로 시그니처에 인자가 하나 늘어난다.

**완료 기준**: RFC 6902 예제 문서 전체가 변환되고 변환 실패 케이스가 없다.

---

## 5. patchwire (나중)

serialize된 wire format 바이트 배열에 `Patch`를 직접 적용한다. **메시지 인스턴스에 대한 적용과 독립적인 구현이어야 한다** — 하나는 `protoreflect.Message`를 다루고 다른 하나는 `[]byte`를 스캔하므로 적용 엔진을 공유할 방법이 없다.

**다만 독립이어야 하는 것은 *적용 엔진*이지 *스키마 해석*이 아니다.** `Range` 정규화, `Field` 제약 해석, 구조 검증, 중복 검출, `MapKey` 범위 판정은 대상이 메시지든 바이트든 답이 같아야 한다. 이것이 갈라지면 구 구현에서 세 백엔드가 같은 Delta를 다르게 읽던 문제가 그대로 재발한다.

→ `patchwire`는 `internal/spec`을 공유하고 적용만 독자적으로 구현한다. `conformance/` 코퍼스를 `patchproto`와 함께 돌려 두 구현이 같은 답을 내는지 고정한다.

> 구 `ref/`, `target/`은 이 작업에 쓸 수 없어 삭제했다. `path`/`targets`가 불투명한 `bytes` 필드였던 더 이전 설계의 인코더였고(`ref.Ref` → `FieldSegment` → 지금의 `Key`), `ref.DecodeInt`는 protobuf varint가 아니라 리틀엔디언 고정 폭을 읽는 자체 인코딩이다. patchwire는 `protowire`를 직접 쓰게 된다. 참조본 `2a96ef1`에 남아 있다.

---

## 6. 테스트 전략

### 6.1 적합성 코퍼스

`patchwire`가 예정되어 있으므로 두 구현이 갈라지지 않게 하는 유일한 방어선이다.

```
conformance/
  cases/*.textproto    (patch, input, want | want_error_code)
  run.go               모든 구현이 같은 코퍼스를 돌린다
```

케이스는 **텍스트 파일이지 Go 코드가 아니어야 한다** — 구현이 추가될 때 케이스를 다시 쓰지 않기 위해서다. `patchproto` 하나뿐일 때도 코퍼스로 시작한다. 나중에 붙이면 이미 갈라진 뒤다.

### 6.2 fail-closed는 검증 표면을 늘린다

21개 조항 각각에 거부 테스트가 필요하다. **정상 경로 테스트보다 거부 테스트가 많은 것이 정상이다.** 구 구현의 테스트 3,243줄은 거의 전부 정상 경로였고, 그래서 fail-open 결함이 전부 통과했다.

### 6.3 속성 테스트

`Diff`가 없으므로 구 구현의 왕복 속성 테스트는 쓸 수 없다. 대신:

| 속성 | 왜 |
|---|---|
| **실패한 `Apply`는 입력을 바꾸지 않는다** | 원자성 계약 그 자체. 가장 중요하다 |
| `test`만 담은 `Patch`는 절대 변경하지 않는다 | `test`의 무변경 보장 |
| `Validate`가 거부하는 `Patch`는 `Apply`도 거부한다 | 두 경로의 일관성 |
| `Patch`를 wire 왕복시켜도 결과가 같다 | `Patch` 자체가 메시지라는 전제 |
| `Apply`는 결정적이다 | 맵 순회 순서에 의존하지 않음 |

### 6.4 퍼즈

임의 바이트 → `Validate` → panic 없음. 임의의 well-formed `Patch` → `Apply` → panic 없고, 실패 시 원본 무손상.

### 6.5 참조본에서 포팅할 시나리오

API는 죽었지만 **무엇을 검증해야 하는가**는 살아 있다. `/workspaces/github.com/lesomnus/protobuf-diff`에서:

| 파일 | 가져올 것 |
|---|---|
| `patchproto/root_test.go` | 컨테이너 스코프 엣지 케이스 |
| `patchproto/list_test.go` | 음수 인덱스, 다중 타겟 |
| `patchproto/map_test.go` | 맵 키 타입별 케이스 |
| `dpb/path_test.go` | 경로 탐색 케이스 |
| `jsonpatch/*_test.go` | 그대로 유효 (스키마 무관) |

---

## 7. 순서 요약

```
P0 에러 분류      ← 선행. 다른 모든 것이 이걸 반환한다
P1 빌더           ← 조기. 이후 모든 테스트가 이걸로 Patch를 만든다
P2 구조 검증      ← unknown field 순회가 forward-compat의 실질적 집행자
P3 값 모델        ← P2와 병렬 가능
P4 주소 해석      ← vacancy를 타입으로. P3과 병렬 가능
P5 적용           ← P0–P4 전부 필요
P6 RFC 6902 변환  ← P5 필요
─────────────────
patchwire         ← 별도. internal/spec 공유, 적용만 독자 구현
```

**적합성 코퍼스는 P5와 함께 시작한다.**
