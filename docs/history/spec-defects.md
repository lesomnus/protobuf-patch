# Patch 스키마 정의 결함 (spec 한정)

`proto/patch/{delta,segment,value}.proto`를 **정의 그 자체로만** 평가한 문서. 구현과의 괴리는 다루지 않는다.

> 관련 문서: [schema-review.md](schema-review.md) — 구현 괴리를 포함한 전체 검토. 여기서 보류한 항목의 근거와 재현 사례는 그쪽에 있다.

---

## 0. 판정 기준

**구현을 전부 지우고 `.proto` 세 파일만 남겼을 때**, 유능한 구현자가

- **(a) 결정할 수 없는 것** → `미정의`
- **(b) 모순되게 결정할 수밖에 없는 것** → `자기모순`
- **(c) 표현할 수 없는 것** → `표현력`
- **(d) 확장 시 파손이 예정된 것** → `진화`

만 정의 결함으로 본다. **명세는 명확한데 코드가 어긋난 것은 여기 넣지 않는다** (§6에 목록만 유지).

이 기준을 적용하면 전체 검토의 21건 중 **20건이 정의 결함**, 그중 상당수는 "구현이 틀렸다"가 아니라 **"명세가 함수를 결정하지 않는다"**로 재진술된다. 구현 괴리는 대부분 이 미정의의 *증상*이었지 원인이 아니었다.

---

## 1. 명세의 전량

| 파일 | 규범 문장 | 내용 |
|---|---|---|
| `delta.proto` | **1** | `:15` — root 규칙 한 문장. `:21`은 일곱 연산 oneof 자리의 빈 `//` |
| `segment.proto` | **13** + 예시 4 | `Segment` arm 3, `FieldSegment` 6, `RangeSegment` 4 + 예시 4 |
| `value.proto` | **1** | `:35` — `// Null value.` |

7개 연산 × 4개 컨테이너(메시지 필드 / 리스트 인덱스 / 맵 키 / 컨테이너 root) = **28개 조합의 의미를 정의해야 하는 형식**이, 총 15개 문장으로 기술되어 있다. 그중 연산의 의미를 다루는 문장은 **하나**(`delta.proto:15`)이고, 그 하나도 부정확하다(→ S3).

**결론: 정의는 타당성 이전에 분량 자체가 성립하지 않는다.** 아래 목록은 그 공백을 메우기 위한 결정 목록이다.

---

## 2. 수정 순서

의존 관계 순이다. **위를 결정해야 아래를 쓸 수 있다.**

```
1단계  실패 계약        S1  ← 다른 모든 조항의 문장 형태를 결정한다
2단계  주소 모델        S2 S3 S4 S5 S6 S7
3단계  값 모델          S8 S9 S10 S11 S12 S13
4단계  연산 의미        S14 S15 S16   ← 1~3이 정해져야 쓸 수 있다
5단계  진화 장치        S17 S18 S19 S20
```

---

## 3. 1단계 — 실패 계약

> 이것을 먼저 정해야 하는 이유: 나머지 모든 조항이 "…하지 않으면 **어떻게 되는가**"로 끝나기 때문이다. 실패의 기본값이 `오류`인지 `무시`인지 정하지 않으면 아래 어떤 문장도 완성할 수 없다.

### 🔴 S1. 실패 계약이 정의되어 있지 않다 · `미정의`

명세가 답하지 않는 질문:

| 질문 | 명세의 답 |
|---|---|
| 세그먼트가 아무 위치로도 해석되지 않으면? | 없음 |
| 세그먼트 종류가 컨테이너에 맞지 않으면? (맵에 `range`, 메시지에 `index: 0`) | 없음 |
| 리더가 모르는 `Segment.kind` / `Entry.kind` / `Value.kind` arm을 만나면? | 없음 |
| `Value`의 종류가 대상 필드 종류와 맞지 않으면? | 없음 |
| `Entry` 하나가 여러 타겟 중 일부에서만 실패하면? | 없음 |
| `Delta`의 3번째 엔트리가 실패하면 1·2번 엔트리의 변경은? | 없음 |
| `test` 실패는 무엇을 보장하는가? | 없음 |

`segment.proto:26`의 *"otherwise, the operations will fail"* 이 실패를 언급하는 **유일한 문장**이지만, 이는 `FieldSegment`의 다중 식별자 불일치라는 한 가지 경우에만 적용되며 위 질문 중 어느 것도 답하지 않는다.

**왜 정의 결함인가.** 패치 형식의 핵심 계약은 *"적용되었거나, 거부되었거나"*다. 명세가 실패를 정의하지 않으면 구현자는 관대한 쪽을 고르게 되고, 그 결과인 *"nil을 반환했고 아무것도 바꾸지 않았다"*는 **호출자가 감지할 수도 복구할 수도 없는 유일한 결과**다. 그리고 `test`가 존재한다는 사실 자체가 이 형식이 조건부 적용을 의도한다는 뜻인데, 원자성 없이는 `test`가 아무것도 지키지 못한다.

**결정해야 할 것.**

1. **해석 실패의 기본값** — 권장: 오류. 관대함이 필요하면 `bool` 플래그가 아니라 `Entry.on_missing` enum(기본 `FAIL`)으로 **와이어에 기록되는 작성자 결정**으로 만들 것.
2. **미지 arm의 처리** — 권장: 오류. 이유는 S17 참조.
3. **원자성 단위** — `Delta` 전체인가, `Entry` 단위인가, 없는가. 권장: *`Delta`는 원자적으로 적용된다 — 구현은 문서 전체를 먼저 검증하거나 스냅샷에 적용 후 성공 시 교체해야 한다.* 그리고 *`Entry`는 타겟 전체에 대해 all-or-nothing이다.*
4. **`test`의 보장** — 해석되지 않는 타겟에 대한 `test`는 통과인가 오류인가. 권장: 오류 (공허한 통과 금지).

---

## 4. 2단계 — 주소 모델

### 🔴 S2. `FieldSegment`가 선택자인지 제약인지 정해져 있지 않다 · `미정의`

`segment.proto:24-26`:

> "FieldSegment represents a field segment in the path, which can be identified by **either name or number**."
> "**One of** name, name_alt, or number must be specified."
> "If multiple fields are specified, **all of them must match** ...; otherwise, the operations will fail."

세 문장이 서로 다른 모델을 기술한다.

- 첫 문장 — *"name **or** number로 식별된다"* → **선택자** (어느 하나로 위치를 찾는다)
- 셋째 문장 — *"모두 일치해야 한다"* → **제약** (찾은 뒤 나머지로 검증한다)
- 둘째 문장 — *"하나는 반드시 지정되어야 한다"* → 최소 요구만 말할 뿐 해석 절차를 말하지 않는다

**제약 모델이라면** 명세가 답하지 않는 것: 무엇을 기준으로 먼저 해석하는가? `name`으로 찾은 필드의 번호가 `number`와 다르면 오류인데, 애초에 `number`로 찾았다면 다른 필드가 나왔을 것이다. **해석 기준 필드가 지정되지 않으면 "모두 일치"는 절차가 아니라 술어일 뿐이다.**

**선택자 모델이라면** 답하지 않는 것: 우선순위는? `name`이 해석에 실패하면 `number`로 폴백하는가, 아니면 실패인가?

**추가 공백:**

| 질문 | 명세 |
|---|---|
| `name`, `name_alt`, `number`가 **전부 미설정**이면? | 없음 (`:25`는 "must be specified"라고만 하고 위반 시를 말하지 않음) |
| `name_alt`는 해석에 참여하는가, 검증에만 쓰이는가? | 없음 |
| `name_alt`가 `:31`의 "ProtoJSON 이름" 외의 값이면? | 없음 |
| 빈 문자열 `name`은 "이름 미지정"인가 "빈 문자열 키"인가? | 없음 (→ S19와 연동) |

**결정해야 할 것.** 선택자 / 제약 중 하나를 고르고 **구조로 인코딩**할 것.
- *제약이라면*: 해석 기준 필드를 명시(권장: 첫 설정 식별자)하고, 나머지 설정 식별자를 해석 결과에 대해 검증하며, 불일치는 **구별 가능한 오류**임을 명시.
- *선택자라면*: `:26`의 "otherwise the operations will fail" 문장을 **삭제**하고 우선순위를 규범으로 명시.

어느 쪽이든 전부 미설정인 `FieldSegment`의 의미를 정의할 것 (권장: 오류).

> `FieldSegment`는 `Path`에 허용된 유일한 타입(`:8`), 모든 `KeyValue`의 키 타입(`value.proto:14`), `move`/`copy`의 소스 타입(`delta.proto:27-28`)이다. **이 결정이 스키마의 절반을 좌우한다.**

---

### 🔴 S3. `number` / `index` 하나가 네 네임스페이스를 겸한다 · `미정의`

| 사용처 | 합법 도메인 | 명세 |
|---|---|---|
| 메시지 필드 번호 | `1 .. 2^29-1` | `Segment.index`는 `:15`에 있음 · `FieldSegment.number`는 `:33`에 있음 |
| 리스트 인덱스 | 모든 정수 (음수 = 뒤에서부터) | `Segment.index`는 `:15`에 있음 · **`FieldSegment.number`에는 없음** |
| 정수 맵 키 | 모든 정수 | **없음** |
| 문자열 맵 키 | 임의 문자열 | **없음** |

`FieldSegment.number`(`:33`)는 *"Field number of the message field."* 라고만 말한다. 그런데 `Path`는 `repeated FieldSegment`이므로(`:8`) **리스트를 통과하는 모든 경로가 `number`를 인덱스로 쓸 수밖에 없다.** 명세는 이 용법을 언급하지 않는다.

**답하지 않는 질문:**

- 음수 인덱스는 무엇을 뜻하는가? `RangeSegment` 주석(`:41`)에서만 간접적으로 드러난다.
- `-1`은 "마지막 원소"인가 "마지막 다음"인가? — **연산마다 다를 수 있는데 명세에 없다.** `remove -1`(마지막 제거)과 `insert -1`(끝에 추가)은 서로 다른 규약을 요구하며, 어느 쪽을 의도했는지 기록하는 인코딩이 없다.
- 문자열 키 맵에 숫자 세그먼트를 주면? 오류인가, 문자열화인가.
- 메시지에 `index: 0`을 주면? 필드 번호 0은 불법이므로 오류여야 하는데 명시가 없다.

**결정해야 할 것.** 네 해석을 부호 규칙과 함께 `segment.proto`에 규범으로 열거하거나, **`FieldSegment`에 `Segment`가 이미 가진 oneof 규율을 부여**할 것:

```proto
message FieldSegment {
  oneof key {
    string name   = 1;   // 필드 이름 또는 문자열 맵 키
    sint64 number = 3;   // 메시지 필드 번호 (>= 1)
    sint64 index  = 4;   // 리스트 인덱스 또는 정수 맵 키
    bool   flag   = 5;   // bool 맵 키 (→ S13)
  }
  string name_alt = 2;   // 수식자
}
```

이러면 리스트 인덱스가 필드 번호와 구별되고, bool 맵 키가 표현 가능해지며(S13 해소), 빈 문자열 키가 "이름 미지정"과 구별된다(S19 해소). `insert`에는 **고유한 append 앵커**를 주어 "끝에 추가"가 매직 값 `-1`이 아니라 별도 인코딩이 되게 할 것.

---

### 🔴 S4. `RangeSegment`의 명세가 함수를 결정하지 않는다 · `자기모순`

`segment.proto:37-43`이 `RangeSegment`의 명세 **전부**다.

```
// If the field is empty, the range is open to that side.
//
//	xxxxx [_, _) == [0, _) matches all elements.
//	---xx [-2, _) matches last two elements.
//	xx-xx [-2, 2) matches last two elements and first two elements.
//	xx-xx [-2, -4) matches last two elements and up to the fourth last element.
```

**네 예시를 동시에 만족하는 규칙이 존재하지 않는다.**

- 예시 1·2는 표준 반열린 구간 `[begin, end)` + 음수는 `len+v`로 설명된다.
- **예시 3은 그 규칙을 위반한다.** `[-2, 2)`는 정규화하면 `[len-2, 2)`이고, `len=5`면 `[3, 2)` = 공집합이다. "마지막 둘 + 처음 둘"을 얻으려면 **wrap-around 합집합** 규칙이 필요하다.
- **예시 4는 wrap-around와도 모순된다.** `[-2, -4)` = `[len-2, len-4)` = `[3, 1)`. wrap-around면 `{3, 4, 0}` = `x--xx`인데, 주석이 스스로 그린 다이어그램은 `xx-xx` = `{0, 1, 3, 4}`다.

즉 예시 3은 예시 1·2의 규칙을 부정하고, 예시 4는 예시 3의 규칙을 부정한다. **세 개의 서로 다른 규칙이 하나의 주석 안에 있다.**

**추가 공백 — "empty"의 정의.** `:38`의 *"If the field is empty, the range is open to that side"* 에서 "empty"가 **필드 미설정**인지 **값이 0**인지 말하지 않는다. 이 구분이 실질적이다:

- 미설정이라면 → 명시적 `[0, 0)`은 공집합
- 값 0이라면 → `[0, 0)`이 "전체"가 되어 **공집합을 표현할 방법이 사라진다**

`begin`과 `end`가 같은 규칙을 쓰는지도 말하지 않는다.

**왜 정의 결함인가.** `RangeSegment`는 이 스키마가 JSON Patch **위에 얹은 자체 확장**이다. 사용자가 직관을 대조할 RFC가 없고 주석이 전부인데, 그 주석이 **구현자에게 함수를 지정하지 못한다.** 세 명의 구현자가 세 개의 다른 동작을 만들어도 전부 "명세를 따랐다"고 주장할 수 있다.

**결정해야 할 것.** 예시가 아니라 **하나의 전역 규칙**을 규범 텍스트로 쓸 것. 권장:

> `begin` 미설정 = 0. `end` 미설정 = 컬렉션 길이. 음수 값은 `len + v`로 정규화. 정규화 후 `[0, len]`로 clamp. `begin >= end`면 공집합.

그리고 **예시 3과 4를 삭제**할 것. 머리+꼬리 합집합이 정말 필요하다면 그것은 **두 개의 범위**이며 `targets`에 두 엔트리로 들어가야지 하나의 구간에 밀어 넣을 것이 아니다. "empty"를 **필드 미설정**으로 정의하고 S19를 함께 못박을 것.

---

### 🟡 S5. `targets`의 컬렉션 의미가 정의되어 있지 않다 · `미정의`

`delta.proto:19`는 `repeated Segment targets = 2;` 뿐이다. 답하지 않는 질문:

| 질문 | 왜 실질적인가 |
|---|---|
| 순서가 의미를 갖는가? | `move`는 순서 의존적일 수 있고 `remove`는 아니다 |
| 중복 타겟은? | `remove [0,0]`은 하나를 지우는가 둘을 지우는가. `insert [0,0]`은 하나를 넣는가 둘을 넣는가 |
| 인덱스/키는 **어느 시점 상태**에 대해 해석되는가? | 원본 컨테이너인가, 앞선 타겟이 적용된 중간 상태인가. 리스트의 `remove`/`insert`에서 결정적이다 |
| 타겟이 0개면? | → S6 |

`remove`가 집합으로, `insert`가 다중집합으로 동작하는 것은 **각각 그럴듯하지만 명세가 어느 쪽도 말하지 않으므로 둘 다 근거가 없다.**

**결정해야 할 것.** `delta.proto`에 명시: *`targets`는 순서 없는 집합이며, 중복은 무효(또는 축약)이고, 모든 타겟 인덱스/키는 **엔트리 시작 전 컨테이너 상태**에 대해 해석된다.* 순서가 의미를 갖는 연산이 있다면(다중 타겟 `move`) 그 연산의 순서 규칙을 별도로 규범화할 것.

---

### 🟡 S6. root 연산이 "부재"로 인코딩되어 있다 · `미정의`

`delta.proto:15`:

> `// If no path nor targets are specified, the op applies to the root.`

**문제 1 — 문장이 부정확하다.** 실제로 의도된 규칙은 *"**targets**가 없으면 `path`가 도달한 컨테이너에 적용된다"*이다. `path`가 있고 `targets`가 없는 경우 — 즉 **중첩 컨테이너 전체를 대상으로 하는 경우** — 가 이 문장으로는 표현되지 않는다. 이 일반화는 `docs/root-replace-plan.md:33`이 인정하고 있으나 `.proto`는 갱신되지 않았다.

**문제 2 — `repeated`에는 presence가 없다.** 따라서 *"타겟을 0개 계산했다"*(프로듀서 버그, 필터링, 부분 디코딩의 자연스러운 결과)와 *"의도적으로 컨테이너를 지목했다"*가 **바이트 수준에서 동일**하다. 그리고 명세는 그 상태에 **가장 파괴적인 의미**를 배정한다 — `Entry{remove: true}`는 가장 짧은 well-formed remove 엔트리이면서 **컨테이너 전체 삭제**다.

**문제 3 — 28개 조합 중 정의된 것이 0개다.** 7 kind × 4 컨테이너(메시지/리스트/맵/root)의 의미가 `.proto`에 없다. root `move`/`copy`가 무엇을 뜻해야 하는지조차 미정의다(→ S14).

**결정해야 할 것.**

1. `:15`를 실제 규칙으로 수정: *"If no targets are specified, the op applies to the container reached by `path` (the root message when `path` is empty)."*
2. **root를 공백에서 유도하지 말 것.** 명시적 표지를 도입:
   ```proto
   // targets와 같은 oneof
   bool at_container = 9;
   // 또는 presence를 갖는 래퍼
   message Targets { repeated Segment segments = 1; }
   ```
   후자는 **부재 = 컨테이너, 존재하나 빈 것 = 정의된 no-op**으로 구분된다.
3. 타겟도 표지도 없는 엔트리는 오류로 규정.

---

### ⚪ S7. `path`와 `targets`의 타입 비대칭에 근거가 없다 · `미정의`

`Path`는 `repeated FieldSegment`(`:8`)인데 `targets`는 `repeated Segment`(`delta.proto:19`)다. `Segment`는 name / index / `FieldSegment` / `RangeSegment` 네 가지가 될 수 있지만 `Path`는 `FieldSegment`만 담는다.

**따라서 `path`로는 범위를 지정할 수 없다.** 이 제약 자체는 방어 가능하다 — path는 정확히 하나의 컨테이너로 해석되어야 하고 범위는 다치(multi-valued)이므로. **그러나 명세가 그 근거를 기록하지 않으므로**, 구현자는 이것이 의도인지 누락인지 알 수 없고 `Path`가 `Segment`를 받아야 하는지 판단할 수 없다.

`segment.proto:18`의 *"FieldSegment can be used over name or index to select the field more precisely"* 는 이 혼란을 키운다 — `SegField`가 `SegName`/`SegIndex`와 교환 가능하다는 뜻으로 읽히지만, 그렇다면 `Path`가 `Segment`를 받지 못할 이유가 더 불분명해진다.

**결정해야 할 것.** 비대칭의 근거를 `segment.proto`에 한 문장으로 기록할 것: *"`Path` segments must resolve to exactly one location; multi-valued segments (`range`) are therefore permitted only in `targets`."*

---

## 5. 3단계 — 값 모델

### 🔴 S8. `Value`가 자기서술적이지 않다 — `Struct`가 메시지인지 맵인지 알 수 없다 · `미정의`

`value.proto:29`의 `Struct m = 10`이 **유일한 키 컨테이너 형태**이며, 메시지와 맵이 동일하게 인코딩된다. repeated 필드가 `Value.l`이 되는 것도 인코더가 이미 대상이 repeated임을 알았을 때뿐이다.

따라서 **`Value` 하나만 놓고는 그것이 무엇인지 결정할 수 없다.** 대상 field descriptor가 있어야만 해석된다.

`docs/root-replace-plan.md:58`이 이를 의도적 결정("모호성 없음")으로 기록한다. **patchproto 내부에서는 실제로 일관적이다.** 문제는 이 결정이 `README.md:50-51`이 내세우는 전제 — *"delta는 다른 메시지처럼 직렬화·저장·전송할 수 있다"* — 와 충돌한다는 점이다.

**정의 수준의 귀결:**

- 스키마 없는 소비자는 `Value`를 **렌더링도 검증도 할 수 없다.**
- 잘못 조준된 delta를 **거부할 수 없다.** 메시지용 `Struct`를 맵 경로에 적용해도 well-formed이므로 재해석될 뿐 거부되지 않는다. **형식에 오조준을 감지할 잉여(redundancy)가 없다.**
- `Value`가 어떤 대상에 유효한지 판단하려면 항상 외부 정보가 필요하므로, `test`/`assign`의 타입 안전성을 명세만으로 규정할 수 없다.

**결정해야 할 것.** `Value`를 자기서술적으로 만들 것.

```proto
// (a) oneof 분리 — 권장
Struct   m   = 10;   // 메시지 필드
MapValue map = 12;   // 맵 항목 (자체 메시지, 역시 repeated KeyValue)

// (b) 또는 Struct에 판별자
enum StructKind { MESSAGE = 0; MAP = 1; }
```

그러면 불일치를 강제 변환 대신 **거부**할 수 있고, 스키마 없는 리더도 최소한 형태를 렌더링·검증할 수 있다.

---

### 🔴 S9. 세 숫자 캐리어가 protobuf 수치 도메인을 모델링하지 못한다 · `미정의`

`value.proto:21,25-26`의 `double f` / `sint64 i` / `uint64 u`는 **태그가 없어서 자신이 어떤 proto 스칼라 종류에 유효한지 말할 수 없다.**

명세가 답하지 않는 질문:

| 질문 | 결과 |
|---|---|
| `i`를 `bool` 필드에 쓸 수 있는가? | 미정의 |
| `i`를 `double` 필드에 쓸 수 있는가? 그 역은? | 미정의 |
| `u`가 `int64` 범위를 넘으면? | 미정의 (절단? 오류? 2의 보수?) |
| `f`가 `float` 범위를 넘으면? | 미정의 |
| `i`가 `int32` 범위를 넘으면? | 미정의 |
| `enum`에는 `i`인가 `u`인가? | 미정의 — **`.proto` 어디에도 enum 언급이 없다** |

**enum이 특히 나쁘다.** `protoreflect.EnumNumber`는 int32이고 **음수일 수 있다.** enum 값 `-3`을 `i`로 쓰면 `sint64 i: -3`, `u`로 쓰면 `uint64 u: 18446744073709551613`이다. 둘 다 같은 enum으로 디코딩되지만 **`proto.Equal`은 false**다.

즉 **하나의 논리적 값에 두 개의 비동등 인코딩이 존재한다 → `Delta`가 정규형(canonical)이 아니다.** 멱등성 검사, 중복 제거, content-addressed 저장, 두 delta의 비교가 전부 무너진다.

**결정해야 할 것.** 둘 중 하나.

- **(a) 캐리어를 자기서술적으로**: `sint32 i32; sint64 i64; uint32 u32; uint64 u64; float f32; double f64; sint32 e;` — 리더가 변환 없이 Value를 필드 종류에 대해 검증할 수 있다.
- **(b) 세 캐리어 유지 + 규범 명시**: proto 종류마다 **정확히 하나의 정규 캐리어**(enum → `i`, 절대 `u` 아님), 그리고 *모든 변환은 범위 검사 후 절단 대신 오류*.

어느 쪽이든 **정규형 규칙**을 명시해야 `Delta` 동등성이 의미를 갖는다.

---

### 🔴 S10. `NullValue`가 oneof 미설정과 중복이다 · `자기모순`

`value.proto`에는 "값 없음"을 나타내는 방법이 **두 개** 있다.

1. `NullValue n = 1` (`:20`) — 명시적 null
2. `kind` oneof 자체의 미설정 상태

명세는 **둘의 관계를 말하지 않는다.** 같은 뜻인가? 다른 뜻인가? 하나가 오류인가?

이 중복이 실질적인 이유는 **와이어 크기가 다르기 때문**이다. `Value` 필드를 아예 생략하는 것과 빈 `Value{}`를 보내는 것은 2바이트 차이이며, ProtoJSON 정규화(`{}`)나 일부 프록시를 거치면 **한쪽이 다른 쪽으로 바뀐다.** 명세가 둘을 구별한다면 그 왕복은 의미를 바꾸고, 구별하지 않는다면 왜 둘 다 있는지 설명되지 않는다.

**그리고 이것이 S17(미지 arm)과 결합하면 데이터 손실이 된다.** 리더가 모르는 oneof 필드 번호만 담은 `Value`는 미설정으로 보고된다. 미설정이 "clear"를 뜻한다면, **미래 버전이 쓴 값이 과거 리더에게 "컨테이너를 지워라"로 읽힌다.**

**결정해야 할 것.** 하나만 남길 것.

- `NullValue n = 1`이 clear를 뜻하고, **미설정 `kind`는 오류**로 규정 — 권장. 미지 arm이 clear로 오독되는 경로를 닫는다.
- 또는 `NullValue`를 삭제하고 미설정이 clear를 뜻하게 할 것.

**성립하지 않는 것은 현재 상태 — 둘 다 있으면서 관계가 정의되지 않은 것이다.**

---

### 🟡 S11. `Struct`의 중복 키 의미가 정의되어 있지 않다 · `미정의`

`value.proto:10`은 `repeated KeyValue fields = 1;` — **유일성 제약이 없고 중복의 의미도 없다.**

`{[{a: "1"}, {a: "2"}]}`가 무엇을 뜻하는지 명세가 답하지 않는다: last-wins? first-wins? 오류? 그리고 `test`에서는 중복 키를 가진 `Struct`가 크기 비교에 어떻게 반영되는가?

세 연산(`assign` / `insert` / `test`)이 각자 다르게 답할 수 있고, **명세가 어느 쪽도 배제하지 않으므로 전부 근거가 없다.**

**결정해야 할 것.** *`Struct.fields`의 키는 키 정규화 후 유일해야 한다*고 명시하고 중복은 오류로 규정할 것. 전방 호환을 위해 허용해야 한다면 규칙 하나(protobuf 관례상 last-wins)를 정해 **세 연산에 균일 적용**할 것.

---

### 🟡 S12. `ListValue` / 맵 값의 null 원소 의미가 정의되어 있지 않다 · `미정의`

`value.proto:39-41`의 `repeated Value values = 1;`은 `Value{n: NULL_VALUE}` 원소를 **표현 가능하게 한다.** 명세는 그것이 무엇을 뜻하는지 말하지 않는다.

- 원소의 기본값인가?
- 건너뛰는가? — 그러면 **리스트 길이가 바뀌고 이후 모든 인덱스가 밀린다.** 후속 엔트리의 `SegIndex`가 다른 원소를 가리키게 된다.
- 오류인가?

`KeyValue.value`가 null인 맵 항목도 같은 질문을 받는다.

**결정해야 할 것.** `value.proto`에 명시할 것. `ListValue.values`와 `KeyValue.value` 안의 `n`을 **금지**하거나(권장), 그 위치의 `n`을 *"원소의 기본값"*으로 정의할 것. **호출자가 요청한 길이를 조용히 바꾸는 해석만은 배제해야 한다.**

---

### 🟡 S13. `KeyValue.key`가 `FieldSegment`인 것은 맵 키에 부적합하다 · `표현력`

`value.proto:14`가 `FieldSegment key = 1;`인데, `FieldSegment`는 `segment.proto:24`가 명시하듯 **"메시지 필드"**를 모델링한다. 이를 맵 키 타입으로 재사용한 결과:

| 맵 키 타입 | `FieldSegment`로 표현하면 |
|---|---|
| `bool` | **표현 불가.** `number: 0`/`1`을 쓸 수밖에 없는데 **필드 번호 0은 protobuf에서 불법**이다 — 같은 값이 유효한 맵 키이면서 무효한 필드가 된다 |
| `uint64` (> 2^63) | `number`가 `sint64`이므로 **2의 보수 재해석이 필요**한데, 그런 규칙이 `segment.proto`에 없다 |
| `int32`/`int64` | 가능하나 `:33`("Field number of the message field")과 모순 |
| `string` | 가능 |

또한 키가 **S2의 미해결 선택자/제약 모호성을 그대로 상속**한다 — 맵 키에 `name`과 `number`를 동시에 지정하면 무슨 뜻인가?

**결정해야 할 것.** 목적에 맞는 키 타입을 줄 것:

```proto
message KeyValue {
  oneof key {
    string       s     = 1;   // 문자열 맵 키
    sint64       i     = 3;   // 정수 맵 키
    uint64       u     = 4;   // 부호 없는 맵 키 (전체 범위)
    bool         b     = 5;   // bool 맵 키
    FieldSegment field = 6;   // 메시지 필드
  }
  Value value = 2;
}
```

키 위치에서 S2의 모호성도 함께 제거된다. (S3의 `FieldSegment` oneof 개편을 택하면 그쪽으로 흡수해도 된다.)

---

## 6. 4단계 — 연산 의미

> 1~3단계가 정해져야 쓸 수 있는 조항들이다.

### 🔴 S14. 일곱 연산의 의미가 정의되어 있지 않다 · `미정의`

`delta.proto:21`은 oneof의 문서가 있어야 할 자리의 **빈 `//` 한 줄**이고, 22-31행이 일곱 kind를 **주석 없이** 선언한다.

정의되어야 하는데 되어 있지 않은 것 — **7 kind × 4 컨테이너 = 28개 조합**:

|  | 메시지 필드 | 리스트 인덱스 | 맵 키 | 컨테이너 root |
|---|---|---|---|---|
| `remove` | ? | ? | ? | ? |
| `test` | ? | ? | ? | ? |
| `insert` | ? | ? | ? | ? |
| `assign` | ? | ? | ? | ? |
| `move` | ? | ? | ? | ? (의미가 있기는 한가?) |
| `copy` | ? | ? | ? | ? (동상) |
| `nest` | ? | ? | ? | ? |

특히 답이 없는 핵심 질문:

- **`insert`와 `assign`의 차이는?** presence 개념이 개입하는가? 리스트에서 `insert`는 삽입이고 `assign`은 덮어쓰기인가?
- **`remove`와 `assign: null`의 차이는?** 둘 다 표현 가능한데 관계가 정의되지 않았다 → **하나의 연산에 두 인코딩, 정규형 없음** (S10과 같은 병).
- **`nest`의 내부 `Delta`는 어떤 좌표계를 쓰는가?** 대상이 메시지면 필드, 리스트면 인덱스, 맵이면 키 — 라고 추정되지만 명세에 없다. **이것이 정의되지 않으면 `Delta`는 자신이 어떤 컨테이너 타입에 대한 것인지 말하지 않는 문서가 된다** (S8·S18과 연결).
- **`test`가 무엇을 보장하는가?** (→ S1)
- **`move`/`copy`의 소스가 엔트리의 `path` 적용 전에 해석되는가 후에 해석되는가?**
- **`move`의 퇴화 사례:** 소스와 타겟이 같으면? 소스가 부재하면? RFC 6902 §4.4는 자기 이동을 no-op으로 규정하지만 이 명세는 아무 말이 없다.

**결정해야 할 것.** 28개 조합을 `delta.proto`의 필드 주석으로 옮길 것. **README를 명세가 아니라 파생 문서로 취급**하는 것이 이 항목의 본질이다.

---

### 🟡 S15. `test`의 `null`이 스코프별로 정의되지 않았고, 필드별 부재를 표현할 방법이 없다 · `표현력`

`test`에 `Value{n: NULL_VALUE}`를 주면 무엇을 단언하는가? 명세에 없다. 가능한 해석이 최소 셋이다:

- 컨테이너가 비었다 (root)
- 키/인덱스가 없다 (맵/리스트)
- 필드가 미설정이다 (메시지 필드)

**그리고 세 번째 — "이 필드가 미설정임"은 패치의 대표적 전제조건인데, 이를 표현할 수단이 명세에 없다.** `test`가 값 비교로 정의된다면 presence 검사를 표현할 수 없고, 부재 검사로 정의된다면 "값이 zero임"을 표현할 수 없다.

**결정해야 할 것.** `null`을 균일하게 정의: *`test` 아래의 `null`은 모든 스코프에서 **"대상이 부재한다"**를 뜻한다 — presence를 추적하는 미설정 필드, 없는 맵 키/인덱스, 빈 컨테이너는 통과하고 존재하면 실패.* 값 비교와 presence 검사를 모두 표현해야 한다면 **별도의 kind**(`bool exists = N`)를 고려할 것.

---

### 🟡 S16. `move`/`copy` 소스가 두 번째 위치를 지정할 수 없다 · `표현력`

`delta.proto:27-28`이 `FieldSegment move = 7; FieldSegment copy = 8;`이고, `FieldSegment`(`:27-35`)는 **경로를 담지 않는다.** `Entry`에는 `path`가 하나뿐이므로 소스는 항상 **타겟과 같은 컨테이너 안**이다.

**컨테이너 간 재배치 — RFC 6902가 `from`에 완전한 JSON Pointer를 주는 이유 — 가 구조적으로 표현 불가능하며, 다른 연산의 조합으로도 복구되지 않는다.** (`copy` 후 `remove`로 흉내 내려 해도 `copy` 자체가 컨테이너를 넘지 못한다.)

타입도 모델의 나머지와 비대칭이다. `targets`는 `repeated Segment`(범위나 여러 위치)인데 소스는 단일 `FieldSegment`다. 따라서:

- 리스트 소스를 어떻게 지정하는가? `FieldSegment.number`를 인덱스로 재사용해야 하는데 `:33`은 이를 말하지 않는다 (→ S3).
- bool 키 맵의 소스는 표현 불가 (→ S13).
- repeated/map 필드를 소스로 쓸 수 있는가? **명세에 없다.**

**결정해야 할 것.** 맨 `FieldSegment` 소스를 완전한 위치로 교체:

```proto
message Location {
  Path    path    = 1;   // 미설정 = 엔트리 path와 같은 컨테이너
  Segment segment = 2;
}
```

이로써 리스트·bool 키 맵 소스가 표현 가능해지고, `move`/`copy`가 미래 옵션을 실을 곳이 생기며, **소스가 엔트리 path 이전/이후 중 언제 해석되는지**를 명시할 자리가 생긴다. 컨테이너 간 이동이 의도적으로 범위 밖이라면 **두 필드 옆에 그렇게 명시**하고 repeated/map 소스 제약도 함께 규범화할 것.

---

## 7. 5단계 — 진화 장치

### 🔴 S17. 미지의 oneof arm 의미가 정의되어 있지 않다 · `진화`

이 스키마에는 확장이 예정된 oneof가 셋이다: `Entry.kind`(3-8, 15), `Segment.kind`(1, 2, 12, 13), `Value.kind`(1-4, 7-11). 번호를 띄운 것 자체가 **확장을 예상했다는 뜻**이다.

그런데 **리더가 모르는 arm을 만났을 때의 의미가 정의되어 있지 않다.** protobuf에서 미지의 oneof arm은 "미설정"으로 보고되므로, 명세가 침묵하면 구현자는 자연스럽게 *"미설정이니 건너뛴다"* 또는 *"미설정이니 기본값"*을 택한다.

**패치 형식에서 이것은 데이터 손상 등급의 실패다.**

- 미지의 `Segment` arm을 건너뛰면 → **저장된 Delta의 부분집합만 적용하고 성공을 보고한다.** 명확한 실패보다 엄격히 나쁘다.
- 미지의 `Value` arm이 "미설정"이고 미설정이 clear를 뜻한다면(S10) → **v2가 쓴 값이 v1 리더에게 "컨테이너를 지워라"로 읽힌다.**

**결정해야 할 것.**

1. *"인식되지 않는 oneof arm은 오류이며 문서 전체의 적용을 거부한다"*를 규범으로 명시.
2. `Delta`에 **프로듀서가 선언하는 엄격성 신호**를 추가하고 **어떤 엔트리를 적용하기 전에** 검사하게 할 것:
   ```proto
   repeated string required_features = 3;   // 또는 uint32 min_reader_version
   ```
   그래야 v2 프로듀서가 구버전 리더의 **깔끔한 전체 거부**를 강제할 수 있다.
3. `Value`의 빈 번호 5, 6을 `reserved`로 선언할 것. (1-4가 이미 `google.protobuf.Value`의 null/number/string/bool과 별칭이므로, 5를 쓰면 `struct_value`와 충돌하는 인상을 준다.)

---

### 🔴 S18. `Delta`에 스키마 식별자가 없다 · `진화`

```proto
message Delta {
  repeated Entry entries = 1;
}
```

`delta.proto:10-12`가 전부다. **`type_url`도, 버전도, descriptor 지문도, required-feature 목록도 없다.**

`README.md:50-51`이 형식을 파는 근거가 정확히 이 속성이고 — *"delta는 직렬화·저장·전송할 수 있다"* — **저장된 delta는 그것이 작성된 스키마보다 오래 산다.** 그런데 소비자가

- 이 Delta가 **어떤 메시지 타입**을 대상으로 작성되었는지,
- 자신이 그것을 **충실히 해석할 수 있는지**

를 판단할 수단이 **와이어에 없다.**

S8(Value가 자기서술적이지 않음)과 결합하면 결과는 확정적이다: **필드가 재번호되거나 제거된 스키마에 옛 Delta를 적용해도 거부되지 않고 재해석된다.** 형식에 오조준을 감지할 잉여가 전혀 없다.

**결정해야 할 것.** 최소한 `string message_type = 2;`(루트 메시지의 FQN), 가능하면 descriptor-set 지문과 `required_features`(S17)를 추가하고, **선언된 타입이 대상과 불일치하는 Delta를 거부하도록 요구**할 것.

---

### 🟡 S19. explicit presence 의존이 선언되어 있지 않다 · `진화`

`segment.proto:1`은 `edition = "2023";`이고 `features` 옵션이 없다. 따라서 기본값인 **EXPLICIT presence**가 적용되어 `FieldSegment.name`/`number`와 `RangeSegment.begin`/`end`가 "미설정"과 "0/빈 문자열"을 구별할 수 있다.

**이 구별이 명세의 하중을 받고 있다:**

- `RangeSegment`의 *"If the field is empty, the range is open to that side"*(`:38`)는 **presence 없이는 성립하지 않는다** — 값 0으로는 "열림"과 "0"을 구별할 수 없다 (→ S4).
- 빈 문자열 맵 키(`""`)와 "이름 미지정"의 구별도 presence에 의존한다 (→ S2).

**그런데 스키마 어디에도 이 의존이 기록되어 있지 않다.** 그리고 같은 저장소의 `proto/sample/value.proto:5`가 `option features.field_presence = IMPLICIT;`라는 **한 줄로 그것을 무효화하는 방법을 시연**하고 있다. 누군가 일관성 정리를 명목으로 같은 줄을 `segment.proto`에 추가하면 명세가 조용히 다른 형식이 된다.

**결정해야 할 것.** `proto/patch/segment.proto`에 `option features.field_presence = EXPLICIT;`를 **명시적으로** 추가할 것 — 오늘은 no-op이지만 **요구사항을 고정하고 edition 기본값 변경에서 살아남는다.** `FieldSegment.name`과 `RangeSegment.begin`/`end`에 *unset과 zero/empty가 구별되며 왜 그런지*를 주석으로 남길 것.

---

### 🟡 S20. 버전 없는 `patch` 패키지와 일반적 타입명 · `진화`

세 파일 모두 `package patch;`이고, 스키마가 버전 없는 FQN `patch.Value`, `patch.Struct`, `patch.Path`, `patch.Segment`, `patch.Delta`, `patch.Entry`와 descriptor 경로 `patch/{value,segment,delta}.proto`를 점유한다.

- **충돌 위험**: 같은 바이너리 안의 다른 patch/diff 스키마와 이름이 겹칠 만큼 일반적이다. protobuf 전역 레지스트리는 중복 FQN/파일 경로에서 실패한다.
- **탈출구 부재**: 본 문서가 요구하는 의미 수정 상당수는 **파괴적 변경**이다. 버전이 없으면 갈 곳이 없다.
- 프로젝트 자신의 `buf.yaml`이 STANDARD를 선택하고 있고 `buf lint`가 이를 직접 보고한다 — **선언된 lint 정책이 실패하고 있는 것이지 스타일 취향이 아니다.**

**결정해야 할 것.** **외부 소비자가 생기기 전에** `package patch.v1;` + `proto/patch/v1/`로 이동할 것. descriptor 경로가 `patch/v1/delta.proto`가 되고, 향후 `patch.v2`가 확보된다.

> 이 항목을 마지막에 두었지만, **실제로는 S1~S19의 수정을 시작하기 전에 처리해야 한다.** 대부분이 파괴적 변경이기 때문이다.

---

### ⚪ S21. `bool remove = 3` — 아무것도 하지 않는 유효한 연산 · `진화`

`delta.proto:23`은 일곱 kind 중 **유일한 비-메시지 arm**이다. oneof case가 이미 presence를 나르므로 bool 페이로드는 중복이며, 그 `false` 값은 **`kind`가 `remove`이면서 의미가 정의되지 않은 Entry**를 만든다. (oneof 인코딩상 명시적 `false`도 태그를 와이어에 싣는다 — *"프로듀서가 remove를 골랐고 아무 의미도 두지 않았다"*와 *"case를 영값으로 설정했다"*가 바이트 동일하다.)

스칼라이므로 **수식자를 실을 수 없는 유일한 kind**이기도 하다. `remove`에 옵션("부재 시 실패", "X와 같을 때만 제거")을 추가하려면 새 kind 번호를 태워야 한다.

**결정해야 할 것.** `bool remove = 3;` → `Remove remove = 3;` + `message Remove {}`. 연산이 oneof case만으로 완전히 기술되고, `remove: false`가 표현 불가능해지며, 미래 수식자가 살 곳이 생긴다. **외부 소비자가 생기기 전이면 비용이 0이고, 이후면 kind 번호 하나다.**

---

## 8. 보류 — 구현 괴리

**정의는 명확한데 코드가 어긋난 것들.** 지금은 손대지 않고 유지한다. 근거와 재현 사례는 [schema-review.md](schema-review.md)에 있다.

| 보류 항목 | 전체 검토 # | 관련 정의 결함 |
|---|---|---|
| `FieldSegment` 해석기 3개가 서로 다름 (첫-일치 / 이름만 / 충족 불가 연언) | 1 | S2 |
| 해석 실패·미지 arm이 백엔드마다 다르게 fail-open | 2 | S1 |
| root 판정 기준이 백엔드마다 다름 (원본 개수 vs 디코딩 후 개수) | 3 | S6 |
| `expandListTargets`가 `HasEnd()`를 무시하고 `begin`/`end` 정규화 규칙이 다름 | 4 | S4, S19 |
| `isClearValue`가 미설정 `kind`를 clear로 처리 | 5 | S10, S17 |
| `move`가 타겟 순서에 의존하고 메시지·맵이 불일치, 퇴화 사례가 파괴적 | 6 | S5, S14 |
| 롤백 없음, 해석 안 된 타겟에 대한 `test`가 공허하게 통과 | 7 | S1 |
| 변환 격자가 비대칭이고 범위 초과가 무성 절단 | 8 | S9 |
| root `assign`이 지운 뒤 해석 못한 키를 버림 | 9 | S18 |
| `patchjson`이 `Value.m`을 nil로 디코딩, `diff.go` 인코더가 panic | 10 | S8 |
| `navigateMap`과 `fieldSegmentToMapKey`가 같은 값을 다른 키로 해석 | 11 | S3 |
| `assign: null`이 맵 키에서 panic | 12 | S15 |
| `Struct` 중복 키가 연산마다 다르게 해석 | 14 | S11 |
| `name`이 `Path.Match`에서만 글로브 패턴, `**` 매직 토큰 | 16 | — (전적으로 구현이 발명한 의미. 패턴 언어를 형식에 포함할지는 별도 결정) |
| `navigateMessage`가 presence 규약을 공유하지 않음 | 18 | S19 |
| `move`/`copy` 소스 해석 실패가 조용한 no-op | 19 | S1, S16 |

> **주의.** 위 대부분은 대응하는 정의 결함을 먼저 해소해야 "무엇이 올바른 동작인지" 판정할 수 있다. **S1(실패 계약)을 정하기 전에는 fail-open 항목들을 고칠 수 없다** — 무엇으로 고쳐야 하는지가 미정이기 때문이다.

---

## 9. 요약

| 단계 | 항목 | 성격 |
|---|---|---|
| 1 | S1 | 실패 계약 — **다른 모든 조항의 문장 형태를 결정한다** |
| 2 | S2 S3 S4 S5 S6 S7 | 주소 모델 — S2·S3가 `Path`·`targets`·`KeyValue`·`move` 전부에 파급 |
| 3 | S8 S9 S10 S11 S12 S13 | 값 모델 — S8·S10이 나머지의 전제 |
| 4 | S14 S15 S16 | 연산 의미 — 1~3이 정해져야 쓸 수 있다 |
| 5 | S17 S18 S19 S20 S21 | 진화 장치 — **S20은 실제로 가장 먼저** (나머지가 파괴적 변경이므로) |

**가장 값싼 것부터:** S19(`EXPLICIT` 한 줄), S7(근거 한 문장), S21(빈 메시지로 교체), S20(패키지 이동) 은 결정 비용이 거의 없고 나머지 작업의 여지를 넓힌다.

**가장 파급이 큰 것:** S1과 S2. 이 둘이 정해지면 나머지 조항의 문장이 기계적으로 따라 나온다.
