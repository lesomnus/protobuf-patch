# 진행 상황

[implementation-plan.md](implementation-plan.md)의 단계별 현황.

> 범례: ⬜ 대기 · 🟨 진행중 · ✅ 완료

| 단계 | 상태 | 패키지 |
|---|---|---|
| P0 에러 분류 | ✅ | `patch/errors.go` |
| P1 빌더 | ✅ | `patch/build.go`, `buildkey.go`, `buildvalue.go` |
| P2 구조 검증 | ✅ | `patch/validate.go` |
| P3 값 모델 | ⬜ | `patch/`, `patchproto/` |
| P4 주소 해석 | ⬜ | `patch/`, `patchproto/` |
| P5 적용 | ⬜ | `patchproto/`, `conformance/` |
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
