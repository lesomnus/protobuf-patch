# Patch 스키마 타당성 검토

`proto/patch/{delta,segment,value}.proto`가 패치 문서 형식으로서 **타당하게 정의되었는지**에 대한 검토 보고서.

> 심각도 범례: 🔴 높음 · 🟡 중간 · ⚪ 낮음
>
> 분류: `모순` 스키마 주석과 구현이 어긋남 · `미정의` 스키마가 규정하지 않음 · `모호` 하나의 구조가 둘 이상의 의미 · `표현력` 표현 불가 · `진화` 확장 시 파손 · `이해도` 사용자가 예측 가능하게 오해함

---

## 1. 결론

**타당하게 정의되어 있지 않다.**

핵심 문제는 스키마가 규정하는 양이 아니라, 규정하는 **네 문장 중 세 문장이 거짓**이라는 점이다.

- `segment.proto:25-26`의 "여러 식별자가 지정되면 **모두** 일치해야 하고, 아니면 연산은 실패한다"는 규칙은 **어디에도 구현되어 있지 않다.** 대신 서로 호환되지 않는 세 가지 해석이 공존한다.
- `segment.proto:37-49`의 `RangeSegment` 예시 4개는 **어떤 단일 규칙으로도 동시에 재현되지 않으며**, 그중 2개는 유일한 적용 경로에서 조용한 no-op이다.
- `delta.proto:15`의 root 규칙은 세 백엔드 중 **하나만** 따른다.
- `value.proto`의 `Value`·`Struct`·`KeyValue`·`ListValue`에는 주석이 **하나도 없다** (전체를 통틀어 `NULL_VALUE` 위의 `// Null value.` 한 줄이 전부다).

나머지 — 일곱 연산이 각각 무엇을 하는지, `targets`에 원소가 둘 이상일 때 무엇을 뜻하는지, 어떤 숫자 캐리어가 어떤 proto 타입에 유효한지, 해석 불가능한 타겟이 오류인지, `Delta`가 원자적으로 적용되는지 — 는 스키마 어디에도 없다. 그 결과 **동일한 바이트의 `Delta`가 이 저장소 안에서만도 세 가지 다른 문서**로 동작한다.

```
Delta{entries: [Entry{remove: true}]}   // targets 없음 = root 연산

  patchproto   → 메시지 전체 삭제,      err=nil
  patchjson    → 아무 일도 없음,        err=nil
  patchstruct  → 아무 일도 없음,        err=nil
```

세 구현이 같은 문서를 다르게 읽는다면, 그것은 구현 버그이기 이전에 **문서 형식이 정의되지 않았다는 증거**다. 스키마가 "이 중 무엇이 맞다"고 말하지 않기 때문에 셋 다 틀리지 않았다.

**설계의 전제 자체는 옳다.** 패치 문서를 protobuf 메시지로 만든 것, `nest`로 경로 접두사를 공유하는 것, `targets`로 다중 대상을 표현하는 것은 모두 JSON Patch보다 나은 선택이며 이 검토는 그 어느 것도 반대하지 않는다. 문제는 **메시지가 말하지 않은 것**에 전부 몰려 있다.

---

## 2. 검토 방법

6개의 독립 관점(값 모델 / 주소 모델 / 연산 집합 / RFC 6902 정합성 / protobuf 위생·진화 / 스키마-구현 괴리)으로 감사한 뒤, 각 발견을 **반증 담당**이 원본 코드에 대해 재검증했다.

- 확인됨 **52건** → 중복 병합 후 **21건**
- 기각 **6건** (§7 부록)

본 보고서의 모든 지적은 재현 사례를 동반하며, 🔴 항목은 별도로 원본 코드에서 재확인했다.

---

## 3. 근본 원인

개별 지적은 아래 여섯 가지 원인의 증상이다. **고칠 것은 이쪽이다.**

### 3.1 `.proto`가 명세가 아니다 — 그리고 말하는 부분은 틀렸다

`delta.proto:21`은 일곱 개 연산 oneof의 문서가 있어야 할 자리에 **빈 `//` 한 줄**이다. `remove`/`test`/`insert`/`assign`/`move`/`copy`/`nest` 중 주석이 달린 것은 하나도 없다. 실제 규범은 Go README, Go doc 주석(`dpb/path.go:19-21`의 `**` 글로브), 그리고 한국어 설계 문서에 흩어져 있다.

와이어 포맷의 계약은 **다른 언어의 구현자가 읽는 산출물**이다. 여기서 그 산출물은 거의 아무것도 정의하지 않으며, 말하는 곳에서는 오도한다.

### 3.2 모든 확장점이 거부가 아니라 조용한 no-op으로 퇴화한다 (fail-open)

"이해할 수 없다"는 모든 경로가 오류 대신 `nil`을 반환한다.

| 상황 | 위치 | 결과 |
|---|---|---|
| 해석 불가 타겟 | `message.go:165-167` | `continue` |
| 컨테이너가 모르는 segment 종류 | `message.go:290-303`, `list.go:328-346` | `default` 없음 → 건너뜀 |
| 해석 불가 `move`/`copy` 소스 | `message.go:79-82`, `107-111` | `return nil` |
| 해석 불가 `Struct` 키 | `patch.go:310-313` | `continue` |
| 미지의 `Value.kind` | `patch.go:350-352` | **"clear"로 해석** |

여기에 롤백 없는 in-place 변경이 겹치면, **절반만 적용된 Delta가 성공을 보고한다.** 올바른 동작은 이미 한 곳에 있다 — `mapKeysFromSegments`(`map.go:337-338`)는 미지의 segment에 오류를 낸다. 즉 이것은 정책이 아니라 **표류(drift)**다.

### 3.3 의미를 메시지가 아니라 환경이 공급한다

`Value.m`, `Segment.index`, `FieldSegment.number`, 그리고 `Delta` 자체가 **정확한 대상 descriptor를 손에 쥐어야만** 해석된다. `docs/root-replace-plan.md:58`은 이를 의도적 결정("모호성 없음")으로 기록한다. patchproto 내부에서는 실제로 일관적이다.

그러나 이 결정은 `README.md:50-51`이 내세우는 전제 — *"delta는 다른 메시지처럼 직렬화·저장·전송할 수 있다"* — 와 정면으로 충돌한다. 그 대가는 이미 저장소 안에서 드러난다: descriptor가 없는 두 백엔드는 `Value.m`을 아예 디코딩하지 못하고(`patchjson/patch.go:82-84`가 `nil` 반환 → assign이 파괴적 null 쓰기로 변질), descriptor를 가진 두 인코더조차 서로 다르다.

`Delta`에는 `type_url`도, 버전도, 능력(capability) 표시도 없다. **소비자가 "이 문서를 충실히 해석할 수 없다"는 사실을 감지할 방법이 없다.**

### 3.4 부재(absence)에 의미를 싣고, 파괴적인 해석이 이긴다

| 인코딩 | 부여된 의미 | 사고 시 결과 |
|---|---|---|
| `targets`가 빈 배열 | 컨테이너 전체 대상 (`delta.proto:15,19`) | `Entry{remove:true}` = **전 필드 삭제**. 타겟을 0개 계산한 프로듀서 버그가 곧 전체 삭제 |
| `end == 0` | "끝까지 열림" (`list.go:339`의 `end <= 0`) | 명시적 `[0,0)`(빈 범위)가 **리스트 전체 선택** |
| `Value.kind` 미설정 | "clear" (`patch.go:350-352`) | 미래 oneof arm을 담은 Value가 **컨테이너 삭제**로 디코딩 |

`repeated`에는 presence가 없으므로 "0개를 계산했다"와 "의도적으로 컨테이너를 지목했다"는 **바이트 수준에서 동일**하다. 세 경우 모두, 미정의의 실패 모드가 최소가 아니라 **최대로 파괴적인 쪽**으로 배정되어 있다.

### 3.5 하나의 구조에 여러 해석기 — 그리고 전부 이미 갈라졌다

스키마가 규범적 규칙을 하나도 제시하지 않으므로, 소비자마다 해석을 재구현했고 서로 어긋났다.

| 구조 | 해석기 | 규칙 |
|---|---|---|
| `FieldSegment` | `patch.go:469-489` | 첫 일치 우선 **선언(disjunction)**, 실패 없음 |
| | `navigate.go:64-78` | **이름만**, `name_alt` 무시 |
| | `dpb/path.go:76-104` | **연언(conjunction)** — 단 구조적으로 충족 불가 |
| 맵 키 | `navigate.go:147-161` | `FieldSegment{number:3}` → 키 `""` |
| | `map.go:345-371` | 같은 값 → 키 `"3"` |
| root 판정 | patchproto | 원본 `len(targets)` |
| | patchjson/struct | **디코딩 후** 길이 |
| `Struct` 인코딩 | `dpb/entry.go:34-41` | list/map 분기 |
| | `diff.go:350-352` | 분기 없음 → **panic** |

`name_alt`는 세 해석기 중 정확히 하나에서만 존중된다. 이는 코드 품질 문제가 아니라, **닻이 될 규범이 스키마에 없다는 사실의 직접적 귀결**이다.

### 3.6 미지의 확장은 거부되지 않고 부분 적용된다

`Segment.kind`가 1, 2, 12, 13으로 번호를 띄운 것은 확장을 예상했다는 뜻이다. 그러나 미지의 arm은 `not_set`으로 디코딩되어 **건너뛰어진다.** 따라서 첫 번째 새 segment 종류가 도입되는 순간, 배포된 리더들은 저장된 Delta의 **부분집합만 적용하고 성공을 보고한다.** 이는 명확한 실패보다 엄격히 나쁘다.

---

## 4. 발견 목록

| # | 심각도 | 분류 | 제목 |
|---|---|---|---|
| 1 | 🔴 | 모순 | `FieldSegment`의 연언 규칙이 서로 다른 세 방식으로 구현됨 (문서대로인 것은 없음) |
| 2 | 🔴 | 미정의 | 해석 불가·부적용·미지의 segment가 조용히 건너뛰어지고 성공을 보고 |
| 3 | 🔴 | 모호 | root 연산이 "targets 부재"로 인코딩되고, 세 백엔드가 "부재"의 정의에 불일치 |
| 4 | 🔴 | 모순 | `RangeSegment` 예시가 상호 모순이고, 둘은 조용한 no-op, 두 소비자가 불일치 |
| 5 | 🔴 | 진화 | 미지/미설정 `Value.kind`가 "clear"와 구별 불가 → 미인식 assign이 컨테이너를 삭제 |
| 6 | 🔴 | 모호 | `targets`가 세 가지 컬렉션 의미를 오가고, `move`의 퇴화 사례가 조용히 데이터를 파괴 |
| 7 | 🔴 | 미정의 | 원자성·롤백이 정의되지 않아 `test`가 전제조건 가드로 기능하지 못함 |
| 8 | 🔴 | 미정의 | `i`/`u`/`f`가 protobuf 수치 도메인을 모델링하지 못함 (무성 절단, enum 이중 인코딩) |
| 9 | 🔴 | 진화 | `Delta`에 스키마 식별자가 없고, root `assign`이 지운 뒤 해석 못한 키를 조용히 버림 |
| 10 | 🔴 | 모호 | `Value`가 자기서술적이지 않음 — `Struct`가 메시지 필드인지 맵 항목인지 대상에 의존 |
| 11 | 🔴 | 모호 | `number`/`index` 하나가 네 네임스페이스를 겸하고, `path`와 `targets`가 같은 값을 다르게 해석 |
| 12 | 🟡 | 표현력 | `test: null`이 컨테이너별로 세 의미이고 메시지 필드에서는 충족 불가 |
| 13 | 🟡 | 표현력 | `KeyValue.key`가 `FieldSegment` — 맵 키에는 잘못된 타입 |
| 14 | 🟡 | 미정의 | `Struct`의 중복 키가 세 가지로 해석됨 (통과 불가능한 맵 root `test` 포함) |
| 15 | 🟡 | 미정의 | `ListValue`의 null 원소를 디코더가 조용히 버려 이후 인덱스가 전부 밀림 |
| 16 | 🟡 | 모호 | `name`이 적용 시엔 리터럴, 매칭 시엔 글로브 패턴 (`**` 매직 토큰) — 스키마에 없음 |
| 17 | 🟡 | 이해도 | `.proto`가 일곱 연산 중 하나도 문서화하지 않음 |
| 18 | 🟡 | 미정의 | edition 2023의 explicit presence에 의존하나 스키마가 이를 명시·고정하지 않음 |
| 19 | 🟡 | 표현력 | `move`/`copy` 소스가 맨 `FieldSegment` — 두 번째 위치를 지정할 수 없음 |
| 20 | 🟡 | 진화 | 버전 없는 `patch` 패키지 + 일반적 타입명 — 표준 진화 탈출구를 봉쇄 |
| 21 | ⚪ | 진화 | `bool remove = 3` — `remove: false`가 유효하지만 아무것도 안 하는 연산 |

---

## 5. 상세

### 🔴 1. `FieldSegment`의 연언 규칙이 어디에도 구현되지 않음

`segment.proto:24-26` · `patch.go:469-489` · `navigate.go:64-78` · `dpb/path.go:76-104` — 분류: 모순

> "One of name, name_alt, or number must be specified. If multiple fields are specified, **all of them must match** for the segment to be considered a match; **otherwise, the operations will fail**."

세 해석기 중 이 규칙을 따르는 것은 없다.

```go
// (1) 타겟/Struct/move 소스: 첫 일치 우선 — 실패하지 않음
findFieldByFieldSegment(patch.go:473-488)  // ByName → ByJSONName → ByNumber
```
`Entry{targets:[SegField(FieldSegment{name:"s_1", number:209})], assign: ValS("boom")}` — `s_1`은 필드 109, `s_2`가 209다. 스키마대로면 **실패해야 한다.** 실제로는 `s_1="boom"`, `err=nil`.

```go
// (2) path 탐색: 이름만 봄, name_alt는 아예 없음
navigateMessage(navigate.go:64-73)
```
`PathOf(FieldSegment{name_alt:"m1"})` → `invalid field number: 0` 오류. 동일한 FieldSegment를 **타겟으로 쓰면 정상 해석된다.**

```go
// (3) Match: 연언이지만 구조적으로 충족 불가
FieldSegment.Match(dpb/path.go:77-102)
```
`name`은 `y.Kind == PathEntryField`를, `number`는 `y.Kind == PathEntryIndex`를 요구하는데 `PathEntry`의 Kind는 하나뿐이다. `{name:"s_1", number:109}.Match(PathEntry{Kind:Field, Key:"s_1", Index:109})`는 **연언이 참인데도 false**다. `dpb/path_test.go:182-192`가 이 모순을 기대 동작으로 고정하고 있다.

**왜 중요한가.** `FieldSegment`는 `Path`에 허용된 유일한 타입(`segment.proto:8`), 모든 `KeyValue`의 키 타입(`value.proto:14`), `move`/`copy`의 소스 타입(`delta.proto:27-28`)이다. 그리고 연언 규칙은 **스키마의 유일한 무결성 장치** — 저장된 Delta가 필드명/번호가 바뀐 메시지에 적용되기를 거부하게 만드는 것 — 이다. 스키마 드리프트에 대비해 이름과 번호를 함께 못박은 신중한 사용자가 얻는 것은: 번호가 버려진 채 이름으로 조용히 해석되거나(적용), 조용한 사용 불가(path의 name_alt), 또는 확정적 불일치(Match)다. **문서화된 안전 속성이 실재하지 않고, 실패는 열려 있다.**

**권고.** `FieldSegment`가 *선택자*인지 *제약*인지 결정하고 구조로 인코딩할 것.
- *제약이라면*: 첫 설정 식별자로 해석한 뒤 나머지 설정 식별자를 해석 결과에 대해 검증하고 불일치 시 구별 가능한 오류를 반환. `findFieldByFieldSegment`·`navigateMessage`·`fieldSegmentToMapKey`·`FieldSegment.Match`를 **하나의 공유 해석기**로 통합. `PathEntry`에 세 좌표를 모두 실어 Match가 연언을 평가할 수 있게 할 것.
- *선택자라면*: `"otherwise the operations will fail"` 문장을 삭제하고 우선순위를 규범으로 명시.

어느 쪽이든 **전부 미설정인 `FieldSegment`의 의미를 정의**할 것 (권장: 오류. 현재는 무의미하게 `true`).

---

### 🔴 2. 해석 불가·미지의 segment가 조용히 건너뛰어짐

`delta.proto:19` · `segment.proto:11-22` · `message.go:163-171, 290-304, 79-82, 107-111` · `list.go:326-348` — 분류: 미정의

`Entry.path`도 `Entry.targets`도, 세그먼트가 **아무것도 가리키지 않을 때 / 컨테이너에 맞지 않는 종류일 때 / 리더가 모르는 oneof arm일 때** 무엇이 일어나는지 규정하지 않는다. 세 컨테이너 구현이 셋 다 다르게 답한다 — 메시지는 타겟별로 건너뛰고, 리스트는 조용히 버리고, 맵만 오류를 낸다.

```go
fd := messageFieldBySeg(fields, seg)
if fd == nil { continue }          // message.go:164-167
```
`messageFieldBySeg`(`message.go:290-303`)는 미지의 필드명, `index <= 0`, `Segment_Range_case`, 그리고 (default 부재로) **모든 미래 arm**에 대해 `nil`을 반환한다.

`sample.Value{s_1:"hello"}`에 `remove:true`로 재현 (전부 `err=nil`, 무변화):

| targets | 결과 |
|---|---|
| `[Segment{range:{begin:0}}]` | 무변화 |
| `[Segment{}]` (v2 arm 대역) | 무변화 |
| `[SegName("no_such_field")]` | 무변화 |
| `[SegName("s_1"), Segment{}]` | `s_1`만 삭제 — **요청한 둘 중 하나가 조용히 증발** |

리스트에서는 `SegField(FieldNum(1))`이 `[a b c]`에서 아무것도 선택하지 못하는 반면 `SegIndex(1)`은 동작한다. 이는 `segment.proto:18`("FieldSegment can be used over name or index to select the field more precisely")과 정면으로 모순된다. `move`/`copy` 소스도 같은 방식으로 사라지므로, **소스 필드명 오타 하나가 `move`를 성공한 no-op으로 만든다.**

**왜 중요한가.** 패치 형식의 핵심 계약은 "적용되었거나, 거부되었거나"다. **"nil을 반환했고 아무것도 바꾸지 않았다"는 호출자가 감지할 수도 복구할 수도 없는 유일한 결과**다. 이것이 본 검토의 다른 모든 주소 모호성을 잡힌 오류가 아니라 보이지 않는 데이터 손실로 증폭시키는 장치다.

**권고.** `delta.proto`의 `targets` 옆에 규범으로 명시: *아무 위치로도 해석되지 않는, 또는 적용 대상 컨테이너에 유효하지 않은 종류의, 또는 인식되지 않는 oneof arm을 가진 세그먼트는 오류이며 엔트리를 중단시킨다.* 관대함이 필요하다면 명시적 와이어 인코딩을 줄 것 — `bool optional`이 아니라 `Entry.on_missing`(기본값 `FAIL`) — 그래야 관용이 **기록된 작성자 결정**이 된다. 그 다음 `messageFieldBySeg`와 `expandListTargets`에 `mapKeysFromSegments`와 같은 `default:` arm을 추가.

---

### 🔴 3. root 연산이 "부재"로 인코딩되고 백엔드마다 부재의 정의가 다름

`delta.proto:15,19` · `message.go:12-15` · `patchjson/list.go:21-27` · `patchstruct/struct.go:22-28` — 분류: 모호

`repeated Segment targets`에는 field presence가 없다. 따라서 **"타겟을 0개 계산했다"와 "의도적으로 컨테이너를 지목했다"가 바이트 동일**하며, 스키마는 그 상태에 파괴적 의미를 배정한다.

더 나쁜 것은, 세 백엔드가 root 분기를 **서로 다른 수량**으로 판정한다는 점이다.

```go
patchproto/message.go:12-15   if len(targets) == 0            // 원본 개수
patchjson/list.go:21-27       if len(decodeListTargets(...))  // 디코딩 후 개수
patchstruct/struct.go:22-28   if len(decodeStructTargets(...))// 디코딩 후 개수
```

**결과 1 — 같은 Delta, 세 결과.** `Delta{[Entry{remove:true}]}`: patchproto는 메시지를 비우고, patchjson과 patchstruct는 아무것도 하지 않는다. 셋 다 `err=nil`.

**결과 2 — 엉뚱한 컨테이너에 쓰기.** 백엔드가 표현하지 못하는 타겟이 사라지면 엔트리가 root 분기로 떨어져 **연산이 부모 컨테이너로 재조준된다.**

```go
outer{targets: [SegField(FieldNum(2))],
      nest: inner{targets: [SegName("A")], assign: "CLOBBERED"}}
```
`&Outer{A:"orig", Inner:{B:"b"}}`에 patchstruct로 적용 → `&{A:CLOBBERED Inner:{B:b}}`, `err=nil`. `decodeStructTargets`(`struct.go:11-19`)가 Name 세그먼트만 남기므로 Field로 지정된 타겟이 증발하고, 엔트리가 root로 떨어져 내부 delta가 **한 단계 위의 무관한 필드에** 적용되었다.

이는 또한 **`patchproto.Diff`의 모든 출력을 patchproto 밖에서 무력화한다** — Diff는 타겟을 오직 `SegField(FieldNum(n))`으로만 발행하기 때문이다(`diff.go:45-47`).

또한 `patchjson/list.go:21-27`과 `patchjson/map.go:26-32`에는 `docs/root-replace-plan.md:22-33`이 *"root 연산이 조용히 버려지는 결함"*으로 지목한 바로 그 형태가 그대로 남아 있다. 해당 계획의 P3–P5는 수정 범위를 patchproto로만 한정했다.

**권고.** "root"를 공백에서 유도하지 말 것.
- 명시적 표지를 추가: `targets`와 같은 oneof 안의 `bool at_container = 9;`, 또는 `message Targets { repeated Segment segments = 1; }`로 감싸 **부재 = 컨테이너, 존재하나 빈 것 = 정의된 no-op**으로 구분.
- 타겟도 표지도 없는 엔트리는 오류로 규정.
- root 분기는 **원본 타겟 개수**로만 판정하도록 요구하고, 백엔드가 디코딩할 수 없는 타겟은 하드 오류로 만들어 **디코딩 실패가 root 연산으로 붕괴하지 못하게** 할 것.
- 7 kind × 3 container root 행렬 전체를 `delta.proto`에 규범 텍스트로 옮길 것 (root `move`/`copy`가 반드시 오류여야 한다는 점과 root `nest`의 descriptor 처리 포함).

---

### 🔴 4. `RangeSegment` 예시가 상호 모순이고 절반이 조용한 no-op

`segment.proto:37-49` · `list.go:326-348` · `dpb/path.go:106-139` — 분류: 모순

주석이 `RangeSegment`의 명세 전부인데, **네 예시를 동시에 만족하는 규칙이 존재하지 않는다.**

> ```
> xxxxx [_, _) == [0, _) matches all elements.
> ---xx [-2, _) matches last two elements.
> xx-xx [-2, 2) matches last two elements and first two elements.
> xx-xx [-2, -4) matches last two elements and up to the fourth last element.
> ```

예시 3은 **wrap-around 합집합** 규칙을 요구한다. 그런데 예시 4의 자체 다이어그램 `xx-xx` = {0,1,3,4}는 wrap-around가 주는 결과({3,4,0} = `x--xx`)와 모순된다. **주석이 자기 자신과 일관되지 않는다.**

구현(`list.go:336-344`)은 합집합을 만들 수 없는 단순 전진 루프다.

```go
if begin < 0 { begin = l + begin }
if end <= 0  { end = l + end }          // ← HasEnd()를 보지 않음
for i := begin; i < end && i < l; i++ { ... }
```

`[a b c d e]`에 `remove` 측정:

| 범위 | 문서 | 실제 |
|---|---|---|
| `[_,_)` | 전체 | `[]` ✅ |
| `[-2,_)` | 마지막 둘 | `[a b c]` ✅ |
| `[-2,2)` | 마지막 둘 + 처음 둘 | `[a b c d e]` — **no-op**, `err=nil` |
| `[-2,-4)` | 마지막 둘 + 뒤에서 넷째까지 | `[a b c d e]` — **no-op**, `err=nil` |
| 명시적 `[0,0)` | 빈 집합 | `[]` — **리스트 전체 삭제** |

`begin`은 `< 0`, `end`는 `<= 0`을 쓰므로 두 경계가 정규화 규칙조차 공유하지 않는다. 한편 `dpb/path.go:116-136`은 `HasBegin()`/`HasEnd()`를 존중하고(스키마 문구가 함의하며 edition 2023이 제공하는 presence) **음수 경계를 전부 거부**한다 — 즉 `[-2,_)`는 패처에서는 동작하지만 `Path.Match`에서는 결코 매치되지 않고, 명시적 `end:0`은 저기선 아무것도 매치하지 않고 여기선 "끝까지"를 뜻한다.

**왜 중요한가.** `RangeSegment`는 이 스키마가 JSON Patch **위에 얹은 자체 확장**이다. 사용자가 직관을 대조할 RFC가 없고 주석이 전부인데, 그 주석이 양방향으로 틀렸다: **리스트 양끝을 잘라내려고 쓴 범위는 조용한 no-op이고, 아무것도 선택하지 않으려고 쓴 범위는 리스트를 지운다.** `remove`의 무성 미적용은 진단 없는 데이터 무결성 실패다.

**권고.** 예시가 아니라 **하나의 전역 규칙**을 `.proto`에 규범 텍스트로 쓸 것. 권장: *`begin` 미설정 = 0, `end` 미설정 = 컬렉션 길이, 음수는 `len + v`, 값은 `[0, len]`로 clamp, 정규화 후 `begin >= end`면 공집합.* 그리고 예시 3, 4를 삭제. 머리+꼬리 합집합이 정말 필요하다면 그것은 **두 개의 범위**이며 `targets`에 두 엔트리로 들어가야지 하나의 구간에 밀어 넣을 것이 아니다. 어느 쪽이든 `expandListTargets`가 `<= 0`이 아니라 `HasBegin()`/`HasEnd()`로 분기하게 하고, `range`는 리스트 컨테이너에만 유효함을 명시하며, `RangeSegment.Match`가 같은 규칙을 구현하게 할 것.

---

### 🔴 5. 미지의 `Value.kind`가 "clear"와 구별 불가

`value.proto:18-37` · `patch.go:346-356` · `message.go:225-234` · `list.go:266-272` · `map.go:233-242` — 분류: 진화

```go
func isClearValue(val *dpb.Value) bool {
	if val == nil { return true }
	switch val.WhichKind() {
	case dpb.Value_Kind_not_set_case, dpb.Value_N_case:   // ← 미지의 arm이 여기로
		return true
	...
```

`Value`에는 unknown-kind 상태도, `reserved` 번호도 없다. **이 빌드가 모르는 oneof 필드 번호만 담은 Value는 `Kind_not_set`으로 보고되고, 모든 root 연산이 이를 null/clear로 취급한다.**

재현: 내용이 필드 5(이 스키마가 비워두었고 `reserved`하지 않은 번호)뿐인 Value는 와이어 바이트 `2801`, `not-set`으로 언마샬, 그대로 재직렬화. `Entry{assign: <it>}`은 메시지/리스트/맵 root에서 각각 **빈 메시지 / `[]` / `map[]`**을 `err=nil`로 만든다.

비-root 스칼라 경로만 fail-closed지만(`patch.go:296-298`) 이는 사고이며 root 경로는 거기 도달하지 않는다. 대조적으로 `Entry.kind`는 미지의 arm에 대해 올바르게 실패한다(`message.go:256-257`).

**부수 문제 — `NullValue`의 중복.** `NullValue n = 1`은 oneof 자체의 미설정 상태와 중복이며 둘이 비일관적으로 처리된다. `KeyValue{key:{number:109}}`에서 `value` 필드가 **부재**하면 `s_1`을 지우고 `err=nil`(11바이트). 같은 KeyValue가 **존재하지만 빈 `Value{}`**를 담으면(13바이트, `18 00` 2바이트 추가) `unsupported value kind: not set` 오류. **ProtoJSON `{}` 정규화나 일부 프록시를 거치면 2바이트 차이로 "clear"와 "오류"가 뒤집힌다.**

**왜 중요한가.** 저장된 상태를 변경하는 것이 목적인 형식에서, **"이 값을 이해하지 못하겠다"가 조용히 "컨테이너를 삭제한다"가 되는 것은 데이터 손실 등급의 실패**다. 그리고 이는 `Value`에 arm이 추가되는 바로 그 순간 발생한다 — v2가 쓴 Delta를 v1 리더가 재생하면, 갱신하려던 레코드를 nil 오류와 함께 파괴한다.

**권고.**
1. `Value`에 `reserved 5, 6;` 추가 (필드 1-4는 이미 `google.protobuf.Value`의 null/number/string/bool과 별칭이고 이 스키마는 Struct/ListValue를 10/11로 옮겼다). `Entry`·`Segment`의 남은 빈 번호도 예약해 미래 할당이 의도적이 되게 할 것.
2. `kind` 미설정을 "clear"로 과적하는 것을 중단. `NullValue n = 1`이 이미 clear를 뜻하므로 **미설정 `kind`는 오류**로 규정하고 `isClearValue`가 `Value_N_case`에만 true를 반환하게 할 것. (반대로 `NullValue`를 삭제하고 미설정이 clear를 뜻하게 하는 것도 일관된 선택이다. 성립하지 않는 것은 **한 경로에서는 둘 다 clear이고 다른 경로에서는 하나가 오류인 현재 상태**다.)
3. `Delta`에 프로듀서가 선언하는 엄격성 신호(`repeated string required_features` 또는 `uint32 min_reader_version`)를 추가하고 **어떤 엔트리를 적용하기 전에** 검사하게 하여, v2 프로듀서가 문서 전체의 깔끔한 거부를 강제할 수 있게 할 것.

---

### 🔴 6. `targets`의 컬렉션 의미가 세 갈래이고 `move`의 퇴화 사례가 데이터를 파괴

`delta.proto:19,27` · `list.go:33-48, 90-95` · `message.go:77-104` · `map.go:106-136` — 분류: 모호

`repeated Segment targets`는 순서·중복·인덱스가 어느 시점 상태에 대해 해석되는지를 전혀 말하지 않는데, kind마다 같은 목록을 **집합 / 다중집합 / 순서 있는 수열**로 해석한다.

| kind | 의미 | 재현 (`[foo bar baz]`, targets `[0,0]`) |
|---|---|---|
| `remove` | 집합 (중복 제거, `list.go:90-95`) | 원소 **하나** 제거 |
| `insert` | 다중집합 (타겟당 1회, `list.go:33-48`) | `[z z foo bar baz]` |
| `move` (메시지) | **순서 의존** (`message.go:99-102`) | 아래 참조 |

`move`는 타겟 루프 **안에서** `cleared` 플래그 뒤로 소스를 지운다. 소스 = 필드 109(`s_1="A"`)일 때:

```
targets [209, 109] → s_1="A", s_2="A"   (소스 생존)
targets [109, 209] → s_1="",  s_2="A"   (소스 소멸)
```

**같은 타겟 집합, 다른 순열, 다른 최종 상태.** 게다가 맵의 `move`는 `after()`에서 무조건 지우므로(`map.go:128-131`) 두 순서 모두 소스를 파괴한다 — **메시지와 맵이 동일한 Entry에 대해 불일치한다.**

**퇴화 사례 (전부 `err=nil`):**

| 입력 | RFC 6902 | 실제 |
|---|---|---|
| `targets:[109], move:109` (자기 자신으로 이동) | §4.4 no-op | `s_1=""` — **삭제** |
| `targets:[309], move:209` (`s_2` 미설정) | — | `s_3=""` — **목적지를 지움** (`message.go:90-91`의 `!hasSrc` 분기) |
| `targets:[309], move:<없는 번호>` | — | 완전한 no-op (`message.go:79-82`) |

거의 동일한 두 작성 실수가 정반대 결과를 내고, 어느 쪽도 오류가 아니다.

**왜 중요한가.** `targets`는 RFC 6902 대비 이 스키마의 **간판 확장**이며, 사용자가 Delta를 손으로 쓰려면 반드시 추론해야 하는 구조다. 유능한 사용자는 `README.md:116`("the op is applied to each")을 **순서 무관**으로 읽는다 — 네 kind에는 참이고 `move`에는 거짓이다. 다중 타겟 `move`는 문서화되어 있으므로(README:221-231) 그 부분은 범위가 정해진 결정이지만, **퇴화 사례는 어디에도 문서화되어 있지 않고 전부 독자의 예상과 반대이며 조용히 실패한다.** 특히 소스 부재 사례가 위험하다: 선택적 필드를 재배치하려던 Delta가, 그 필드가 마침 미설정일 때마다 **목적지를 삭제하는 Delta로 변한다.** `Diff`는 `move`를 결코 발행하지 않으므로 이 경로들은 손으로 만든/변환된 delta로만 도달된다 — 즉 **가장 덜 검증된 집단**이다.

**권고.** `delta.proto`에 못박을 것: `targets`는 **순서 없는 집합**이며, 중복은 무효(또는 축약)이고, 모든 타겟 인덱스/키는 **엔트리 시작 전 컨테이너 상태**에 대해 해석된다. 퇴화 사례를 명시적으로 정의: **소스 위치로 해석되는 타겟은 엔트리를 no-op으로 만들고**(RFC 6902 §4.4), **부재하거나 해석 불가한 소스는 오류이지 clear가 아니다** — "목적지를 지운다"는 이미 `remove`라는 고유한 철자를 갖고 있으며 `move`의 창발적 동작이어서는 안 된다. 다중 타겟 `move`를 유지한다면 순서 규칙을 규범으로 명시(모든 쓰기 전에 소스를 한 번 읽고, 모든 쓰기 후에 한 번 지운다)하고 메시지·리스트·맵에 동일하게 구현할 것. 아니면 `move`를 단일 타겟으로 제한하고 `copy`를 다중 타겟 형태로 문서화할 것.

---

### 🔴 7. 원자성이 정의되지 않아 `test`가 가드로 기능하지 못함

`delta.proto:11,19,24` · `patch.go:80-88, 104-111` · `message.go:162-183` — 분류: 미정의

스키마는 RFC 6902의 `test`를 차용했으면서 **문서 수준 원자성도 엔트리 수준 원자성도 정의하지 않으며**, in-place 진입점은 롤백 없이 변경한다.

```go
for i, entry := range delta.GetEntries() {
	if err := o.patch(v, fd, entry); err != nil {
		return fmt.Errorf("entry[%d]: %w", i, err)   // patch.go:105-110 — 롤백 없음
	}
}
```

재현: `Delta{[assign s_1="changed", test i64_1==42, assign s_2]}`를 `{s_1:"orig"}`에 적용 → `entry[1]: ... test failed`를 반환하고 **`s_1="changed"`가 남는다.** 가드가 발동했는데 가드가 막으려던 변경이 살아남았다.

엔트리 **내부**에도 원자성이 없다. `message.go:162-183`은 `errors.Join`으로 타겟별 오류를 모으며 계속 진행하므로, `targets:[s_1(#109), i64_1(#103)], assign:{s:"str"}`은 `s_1="str"`을 남기고 `i64_1`의 kind 오류를 반환한다.

**공허한 통과.** 1원소 리스트의 인덱스 99에 대한 `test "nope"`는 `nil`을 반환하고(`list.go:111-114`), 존재하지 않는 필드명에 대한 `test "nope"`도 `nil`을 반환한다(`message.go:165-166`). **해석되지 않은 타겟은 건너뛰어지므로 아무것도 가리키지 않는 `test`는 통과한다.**

`Patched`(`patch.go:66-73`)는 먼저 clone하므로 우연히 원자적이지만, 이는 **형식의 속성이 아니라 래퍼의 속성**이며 스키마도 README도 어느 쪽 동작도 언급하지 않는다.

**왜 중요한가.** `test`의 존재 이유는 조건부 적용을 안전하게 만드는 것 — optimistic locking / 전제조건 — 뿐이다. 현 명세로는 **아무것도 지키지 못한다**: 앞선 엔트리들은 이미 실행되어 되돌릴 수 없고, 타겟 부재조차 감지하지 못하므로 경로가 밑에서 바뀐 `test`는 성공을 보고한다. RFC 6902 §5(실패한 연산은 문서를 변경하지 않은 채로 두어야 함)의 심상을 가져온 사용자는 **가장 손해가 큰 방식으로 틀린다.**

**권고.** 실패 계약을 `delta.proto`에 명시: *`Delta`는 원자적으로 적용된다 — 구현은 문서 전체를 먼저 검증하거나 스냅샷에 적용 후 성공 시 교체해야 한다.* 그리고 *`Entry`는 타겟 전체에 대해 all-or-nothing이다* (`errors.Join`-후-계속을 fail-fast로 교체). 별도로 **해석되지 않는 타겟은 `test`에 대해 오류**임을 규정해 공허한 통과를 막을 것. in-place 비원자 적용을 남기려면 그것은 주 진입점의 동작이 아니라 **이름 붙은 문서화된 모드**여야 한다.

---

### 🔴 8. `i`/`u`/`f`가 protobuf 수치 도메인을 모델링하지 못함

`value.proto:21,25-26` · `patch.go:177-204, 233-277` · `dpb/entry.go:121-131` · `diff.go:319-326` — 분류: 미정의

태그 없는 세 숫자 캐리어(`double f`, `sint64 i`, `uint64 u`)는 자신이 **어떤 protobuf 스칼라 종류에 유효한지 말할 수 없다.** 그래서 리더는 문서화되지 않은 비대칭 변환 격자를 적용하고 맞지 않는 것을 조용히 절단한다.

`README.md:155-156`은 규범적으로 *"A `Value` whose kind does not match the target field's kind is rejected with an error (never a panic)"*라고 말한다. `checkValueAssignable`(`patch.go:194-195`)이 전반부를 반박한다:

```go
case dpb.Value_I_case, dpb.Value_U_case:
	ok = isNumericKind(k)      // BoolKind, EnumKind 포함
```

| 변환 | 결과 |
|---|---|
| `ValI(7)` → bool | **허용** (true) |
| `ValB(true)` → int32 | 거부 |
| `ValI(1)` → double | **허용** |
| `ValF(1.9)` → int32 | 거부 |

**격자가 비대칭이고, 스키마 어디에도 없으며, 추측 불가능하다.**

**무성 절단 (전부 `err=nil`):**

| 입력 | 대상 | 결과 |
|---|---|---|
| `ValI(1<<40)` | int32 | `0` — 게다가 int32는 implicit presence라 **필드가 출력에서 사라진다** |
| `ValU(1<<63)` | int64 | `-9223372036854775808` |
| `ValF(1e300)` | float | `+Inf` |

**enum 이중 인코딩.** `README.md:150`은 `dpb.ValU(number)`를 규정하고 두 인코더 모두 `ValU(uint64(v.Enum()))`을 쓴다(`dpb/entry.go:122`, `diff.go:320`). 그러나 `protoreflect.EnumNumber`는 int32이며 **음수일 수 있다** — enum 값 -3은 `u: 18446744073709551613`으로 인코딩되어 마샬 크기가 13바이트에서 22바이트로 늘고, `ValI(-3)`은 동일한 enum으로 디코딩된다. `proto.Equal(ValI(2), ValU(2)) == false`.

**왜 중요한가.** 저장·전송되는 패치 형식에서 **무성 수치 절단은 리뷰를 통과해서 살아남는 데이터 손실**이다 — delta는 검증되고, 적용되고, 성공을 보고하고, 0을 쓴다. 비대칭성(int→bool 허용, bool→int 거부; int→float 허용, float→int 거부)은 유능한 사용자가 정확히 틀리는 종류이며, 저자의 설계 목표가 이를 **일급 결함**으로 만든다. 하나의 enum 값에 두 개의 unequal 인코딩이 있으면 Delta 동등성 사용처(멱등성 검사, 중복 제거, content-addressed 저장, 두 delta 비교)가 전부 무너진다.

**권고.** 둘 중 하나.
- **자기서술 캐리어**: `sint32 i32; sint64 i64; uint32 u32; uint64 u64; float f32; double f64; sint32 e;` — 리더가 변환 없이 Value를 필드 종류에 대해 검증할 수 있게.
- **세 캐리어 유지 + 규범 명시**: proto 종류마다 정확히 하나의 정규 캐리어(enum → `i`, 절대 `u` 아님), 모든 변환은 **범위 검사 후 절단 대신 오류**.

올바른 형태는 이미 저장소에 있다 — `patchproto/cast.go`의 `cast`/`ErrInvalidCast` 격자는 명시적이고 이름이 붙어 있다. Value 디코딩에 이를 적용하는 문제다. `README.md:155-156`을 실제 규칙에 맞게 수정하고, `dpb/entry.go:122`·`diff.go:320`의 `ValU(uint64(v.Enum()))`을 고칠 것.

---

### 🔴 9. `Delta`에 스키마 식별자가 없고 root `assign`이 지운 뒤 조용히 버림

`delta.proto:10-12` · `message.go:223-236` · `patch.go:304-319` — 분류: 진화

```go
before := snapshotMessage(c)
clearAllMessageFields(c)                          // ← 먼저 전부 지움
if !isClearValue(val) {
	applyStructToMessage(val.GetM(), c, o.Types)  // ← 그 다음 해석 가능한 것만 적용
}
```
```go
fd := findFieldByFieldSegment(fields, kv.GetKey())
if fd == nil { continue }                          // patch.go:310-313 — 나머지는 조용히 소멸
```

재현: 대상 `{s_1:"old", s_2:"also old"}`, root assign에 `Struct{FieldNum(109)→"new", FieldNum(777)→"lost"}` → **`s_1="new", s_2=""`, `err=nil`.** Delta가 값을 실어 온 필드는 버려졌고, 언급조차 하지 않은 필드는 지워졌으며, 호출자는 패치가 성공했다고 들었다.

`dpb.ValM`이 모든 필드를 **번호로** 인코딩하므로(`dpb/entry.go:46`), 필드가 재번호되거나 제거된 스키마에서 정확히 이 형태가 발생한다.

그리고 `delta.proto:10-12`는 `message Delta { repeated Entry entries = 1; }` — **type_url도, 버전도, required-feature 목록도 없다.**

**왜 중요한가.** `README.md:50-51`이 형식을 파는 근거가 정확히 이 속성이고("delta는 직렬화·저장·전송할 수 있다"), **저장된 delta는 그것이 작성된 스키마보다 오래 산다.** clear-then-partially-apply는 그 상황에서 가능한 최악의 순서다: 실패 모드가 "거부됨"도 "변경 없음"도 아닌 **"데이터가 조용히 사라짐" + nil 오류**다.

**권고.** `Delta`에 스키마 식별자를 추가 — 최소한 `string message_type = 2;`(루트 메시지의 FQN), 가능하면 descriptor-set 지문과 `required_features` 목록 — 하고, 선언된 타입이 대상과 불일치하는 Delta를 거부하도록 요구할 것. 해석 불가한 `KeyValue.key`를 `continue`가 아니라 오류로 만들 것(#2에서 제안한 `on_missing` 정책의 지배를 받게). clear-then-apply를 root assign 의미로 유지해야 한다면, 부분 해석이 **가시적으로 위험함**을 알 수 있도록 `delta.proto`에 규범으로 명시할 것.

---

### 🔴 10. `Value`가 자기서술적이지 않음

`value.proto:9-16,29` · `patch.go:324-341` · `dpb/entry.go:34-97` · `diff.go:314-368` · `patchjson/patch.go:63-85` — 분류: 모호

`Struct m = 10`은 `Value`의 유일한 키 컨테이너 형태이며 **메시지와 맵이 동일하게 인코딩된다.** repeated 필드가 `Value.l`이 되는 것도 인코더가 이미 대상이 repeated임을 알았을 때뿐이다. 따라서 디코딩은 순전히 대상 field descriptor로 분기한다.

`docs/root-replace-plan.md:58`이 이 결정을 명시적으로 기록한다("모호성 없음"). **patchproto 내부에서는 실제로 일관적이다.** 비용은 바깥에 떨어진다.

**(1) descriptor 없는 백엔드는 파괴적으로 fail-open한다.**
```go
// patchjson/patch.go:82-84 — Value_M_case, Value_L_case arm이 없음
default:
	return nil
```
동일한 Delta `{targets:[SegName("m_1")], assign: ValM(sub)}`:
- patchproto → `m_1:{s_1:"deep"}` ✅
- patchjson → `map["m_1": <nil>]`, `err=nil` — **assign이 기존 중첩 객체를 파괴하고 성공을 보고**
- patchstruct → 무변화

**(2) 두 인코더가 이미 갈라졌다.** `dpb.protoMsgToStruct`(`entry.go:34-41`)는 IsList/IsMap로 분기하지만 `patchproto.messageToStruct`(`diff.go:350-352`)는 `toValue(v, fd)`를 무조건 호출한다. `to.m_1`이 map 필드를 포함하면 `Diff(from,to)`가 **panic**하고(`type mismatch: cannot convert map to message`, `diff.go:334`), repeated 필드를 포함하면 Go 포인터가 문자열 Value로 포맷된 **손상된 delta**를 만든다.

**(3) 잘못 조준된 delta가 거부 대신 조용히 강제 변환된다.** `dpb.ReplaceWith(inner)`(필드 번호 109로 키가 매겨진 메시지 struct)를 `map<string,string> m_s_s`를 가리키는 path에 적용하면 `err=nil`로 `map[109:x]`가 나온다.

**권고.** `Value`를 자기서술적으로 만들 것. oneof를 분리 — `Struct m = 10`은 메시지 필드용으로 두고 별도의 `MapValue map = 12`(자체 메시지, 역시 `repeated KeyValue`) 추가 — 하거나, `Struct`에 판별자 추가(`enum StructKind { MESSAGE = 0; MAP = 1; }`). 그러면 `patchMapRoot`와 `applyStructToMessage`가 강제 변환 대신 불일치를 거부할 수 있고, 스키마 없는 리더도 최소한 형태를 렌더링·검증할 수 있다. 별도로, 백엔드가 디코딩할 수 없는 `Value` arm에 대해 `nil`이 아니라 **오류**를 반환하도록 요구하고, `messageToStruct`가 `dpb.protoMsgToStruct`에 위임하게 하여 **인코더를 하나로** 만들 것.

---

### 🔴 11. `number`/`index` 하나가 네 네임스페이스를 겸함

`segment.proto:14-16,33-34` · `navigate.go:70-77,147-161` · `map.go:312-330,345-371` — 분류: 모호

*"Field number of the message field"*로 문서화된 하나의 `sint64`가 동시에 **리스트 인덱스**, **정수 맵 키**, 그리고 (문자열화 폴백으로) **문자열 맵 키**다. 각 도메인의 합법 범위는 `>=1` / 모든 정수 / 모든 정수 / 임의 문자열이다.

**날카로운 지점 — `path`와 `targets`가 같은 바이트를 다른 키로 해석한다.**

```go
navigateMap(navigate.go:148-149)      w = ValueOfString(fs.GetName())      // 무조건 이름
fieldSegmentToMapKey(map.go:352-357)  HasName() ? 이름 : fmt.Sprintf("%d", number)
```
따라서 `PathOf(FieldNum(3))`은 키 `""`를 가리키고, **바이트 동일한** `FieldSegment{number:3}`이 타겟에서는 키 `"3"`을 가리킨다. 키 `"2"`를 가진 `map<string,Value>`에서 검증: 타겟으로는 찾아서 제거(`err=nil`), path 세그먼트로는 `cannot navigate into unset map key `(빈 키) 오류.

`navigateMap`은 저장소에서 `fieldSegmentToMapKey`를 거치지 않는 **유일한** 맵 키 해석기이며, BoolKind 케이스도 없다(`unsupported map key type: bool`) — 타겟 경로는 bool을 처리하는데도.

**도메인 충돌.** `navigate.go:71-73`은 메시지 필드에 `n <= 0`을 거부하지만 `navigate.go:117-120`은 리스트에 0과 음수를 허용한다. `messageFieldBySeg`는 `num <= 0`에 nil을 반환하므로(`message.go:295-298`) 메시지의 `SegIndex(0)`은 조용한 no-op이다.

**`insert`의 off-by-one.** `[a b c]`에서:

| 연산 | 결과 |
|---|---|
| `remove -1` | `[a b]` |
| `assign -1` | `[a b Z]` |
| `insert -1` | `[a b c Z]` ← 다른 규약 |
| `insert -2` | `[a b Z c]` |

`README.md:213`에만 문서화되어 있고 `.proto`에는 없으며, **저자가 어느 규약을 의도했는지 기록하는 인코딩이 없다.** `dpb/json.go:199-211`은 RFC 6901의 `-` 토큰과 리터럴 `-1`을 모두 `int(-1)`로 뭉갠다.

**왜 중요한가.** 컨테이너 상대 주소 지정은 이 형식이 모방하는 JSON Pointer 모델에 내재하므로 **과적 자체는 방어 가능하다.** 방어 불가능한 것은 둘이다. 첫째, `path`와 `targets`는 둘 다 FieldSegment 모양이고 README에서 교환 가능해 보이는데 **같은 바이트를 다른 맵 키로 해석한다** — 판별자 없는 순수한 모호성이다. 둘째, `Segment{index:-1}`이 네 kind에서는 "마지막 원소"이고 `insert`에서는 "마지막 다음"인데, 이는 사용자가 **모든 엔트리마다 만지는** 구조이며 `.proto`로는 대조할 수 없는 off-by-one이다.

**권고.** 최소한 `path`와 `targets`가 **하나의 해석기를 공유**하게 할 것(`navigateMap`이 `fieldSegmentToMapKey`를 호출). `number`/`index`의 네 해석을 부호 규칙과 함께 `segment.proto`에 규범으로 열거하고, 숫자 세그먼트가 문자열 키 맵에서 문자열화된다는 사실을 명시할 것. 더 나은 방법: `FieldSegment`에 `Segment`가 이미 가진 oneof 규율을 부여 —
```proto
oneof key { string name = 1; sint64 number = 3; sint64 index = 4; bool flag = 5; }
// name_alt는 수식자로 유지
```
그러면 bool 맵 키가 표현 가능해지고, 리스트 인덱스가 필드 번호와 구별되며, 키 `""`가 "이름 미지정"과 구별된다. `insert`에는 **고유한 앵커**를 주어 "append"가 매직 값 -1이 아니라 별도 인코딩이 되게 할 것.

---

### 🟡 12. `test: null`이 컨테이너별로 세 의미이고 메시지 필드에서는 충족 불가

`delta.proto:24,26` · `value.proto:20` · `patch.go:215-216,436-442` · `message.go:203-209` · `map.go:61-71`

`ValNull()`이 `test` 아래에서: root에서는 *"컨테이너가 비었다"*, 맵 타겟에서는 *"키가 없다"*, **메시지 필드/리스트 타겟에서는 어떤 메시지도 충족할 수 없는 명제**다. 즉 **필드별 부재라는 패치의 대표적 전제조건에 동작하는 표현이 없다.**

`test:null`은 유효하지 않은 `protoreflect.Value`로 디코딩되고(`patch.go:215-216`), `protoValueEqual`(`patch.go:436-442`)은 한쪽만 유효하지 않으면 false를 반환하는데 `Message.Get(fd)`는 **항상** 유효하다. explicit-presence 필드 `opt_s`(#409)로 검증: **미설정일 때도 실패, 설정일 때도 실패.** 맵 타겟에서는 같은 Entry가 "키 부재"로 동작하며 충족 가능하다(`c.Get(k)`가 없는 키에 대해 무효이므로).

부수적으로 `assign: null`은 `remove: true`와 정확히 같은 코드 경로로 필드를 지운다(`message.go:69-73` vs `25-28`) — **하나의 연산에 두 개의 바이트-다른 인코딩, 정규형 없음.** `Diff`는 `remove`만 발행한다.

**권고.** `null`을 균일하게 정의: *`test` 아래에서 `null`은 모든 스코프에서 "대상이 부재한다"를 뜻한다* — presence를 추적하는 미설정 필드, 없는 맵 키/인덱스, 빈 컨테이너는 통과하고 존재하면 실패. 메시지 필드 케이스를 값 비교가 아니라 `!c.Has(fd)`로 구현하거나 명확한 오류로 거부할 것. `assign`은 `null` 페이로드를 금지하고 clear에 `remove`를 요구하거나, `assign: null`을 `remove: true`의 규범적 별칭으로 선언하고 프로듀서에 정규형 발행을 요구할 것.

> 참고: `assign: null`을 맵 키에 적용하면 현재 `interface conversion: interface {} is nil, not string`으로 panic한다. 코드 버그이나, **null 페이로드 케이스가 명세된 적이 없기 때문에** 존재하는 버그다.

---

### 🟡 13. `KeyValue.key`가 `FieldSegment` — 맵 키에는 잘못된 타입

`value.proto:13-16` · `segment.proto:24-35` · `dpb/entry.go:99-116` · `map.go:345-371`

`FieldSegment`는 *"이름/번호로 식별되는 메시지 필드"*를 모델링한다. 이를 맵 키 타입으로 재사용한 결과:

| 문제 | 근거 |
|---|---|
| bool 키가 **불법 필드 번호**로 인코딩 | `mapKeyToFieldSegment`(`entry.go:106-109`)가 `FieldNum(1)`/`FieldNum(0)`. **필드 번호 0은 protobuf에서 불법**이고 `fields.ByNumber(0)`은 항상 nil — 같은 값이 유효한 맵 키이자 무효한 필드 |
| 2^63 초과 uint64 키가 문서화되지 않은 2의 보수 재해석 | `FieldNum(int64(...Uint()))`(`entry.go:112`) → `uint64(fs.GetNumber())`(`map.go:365`). 키 2^64−1이 `number: -1`로 이동하며, **음수 `number`를 부호 없는 키로 재해석하라는 말이 `segment.proto`에 없다** |
| 숫자 키가 문자열 키로 조용히 강제 변환 | `map<string,V>`에서 `ValueOfString(fmt.Sprintf("%d", ...))`(`map.go:356-357`) → `dpb.Field("7")`과 `dpb.FieldNum(7)`이 같은 키에 도달 |
| #1의 미구현 연언 규칙을 상속 | Struct 키 `FieldSegment{name:"alpha", number:109}`가 `"alpha"`라는 필드가 없는데도 `s_1="V"`를 `err=nil`로 설정 |

bool/정수 맵 키에는 테스트 커버리지가 없다 — `internal/sample`은 문자열 키 맵만 선언하며, `docs/root-replace-plan.md:84`가 이 공백을 인정한다.

**권고.** 목적에 맞는 키 타입을 줄 것:
```proto
message KeyValue {
  oneof key { string s = 1; sint64 i = 3; uint64 u = 4; bool b = 5; FieldSegment field = 6; }
  Value value = 2;
}
```
`field`는 메시지 필드용, 스칼라들은 맵 키용. `u`가 전체 uint64 범위를 복원하고 `b`가 불법 필드 번호 없이 bool 키를 표현 가능하게 한다. 키 위치에서 연언 모호성도 함께 제거된다.

---

### 🟡 14. `Struct`의 중복 키가 세 가지로 해석됨

`value.proto:9-11` · `patch.go:304-319,414-433` · `map.go:243,256,272-291`

`repeated KeyValue fields = 1;`은 유일성 제약을 부과하지 않고 스키마는 중복 키의 의미를 말하지 않는다. `S = {[{a:"1"}, {a:"2"}]}`를 `path=[m_s_s]`에 타겟 없이 적용:

| kind | 결과 | 규칙 |
|---|---|---|
| `assign` | `map[a:2]` | **last-wins** (`mp.Set` 덮어쓰기) |
| `insert` | `map[a:1 ...]` | **first-wins** (`onlyAbsent && mp.Has(mk)` 가드) |
| `test` (맵이 실제로 `{a:"2"}`일 때) | `size 1 != 2` 실패 | **키를 중복 제거하지 않고 `c.Len()`과 `len(want)` 비교**(`map.go:273-275`) → 중복 키를 가진 Struct는 맵 root test를 **결코 통과할 수 없다** |

메시지 root에서 `{[{#109:"first"}, {#109:null}]}`는 `s_1 == ""`를 `err=nil`로 만든다 — 뒤의 null이 앞의 값을 지운다. `value.proto`도, README도, 설계 문서도 중복을 언급하지 않는다.

**권고.** `value.proto`에 *`Struct.fields`의 키는 정규화 후 유일해야 한다*고 명시하고 `applyStructToMessage`·`setMapEntries`가 중복 키에 오류를 반환하게 할 것. 전방 호환을 위해 중복을 허용해야 한다면 규칙 하나(protobuf 관례상 last-wins)를 정해 assign/insert/test에 균일 적용하고, `map.go:273-275`의 크기 비교 전에 `want`를 중복 제거해 위음성을 없앨 것.

---

### 🟡 15. `ListValue`의 null 원소를 디코더가 조용히 버림

`value.proto:9-16,39-41` · `patch.go:396-408,414-433`

```go
if !pv.IsValid() { continue }   // patch.go:402-404 — null 원소, 버려짐
```

`Entry{path:[r_s_1], assign: ValL(ValS("a"), ValNull(), ValS("b"))}` → `err=nil`, **2원소 리스트 `[a b]`.** 호출자는 셋을 요청했다. 이후 `SegIndex(2)`를 지정하는 엔트리는 아무것도 못 맞추고 `SegIndex(1)`은 의도한 원소 대신 `"b"`를 맞춘다. 맵 값도 동일하게 키가 조용히 누락된다.

`docs/root-replace-plan.md:84`가 이를 알려진 한계로 기록하며 *"인코딩은 null을 만들지 않음"*으로 정당화한다. 이는 **라이브러리가 생성한 delta에 대해서는 옳다** (`protoValToVal`이 18개 protoreflect kind를 모두 처리하므로 `default: return nil`은 도달 불가). 따라서 위험은 **손으로 만든 delta에 국한**되지만, `dpb.ValNull()`이 공개 빌더이므로 도달은 자명하다.

**권고.** `value.proto`에서 결정하고 명시할 것. `ListValue.values`와 `KeyValue.value` 안의 `n`을 금지하고 디코더가 `continue` 대신 오류를 반환하게 하거나, 그 위치의 `n`을 *"원소의 기본값"*으로 정의하고 `fd.Default()`를 append하게 할 것. **호출자가 요청한 길이를 조용히 바꾸는 것만은 남아서는 안 된다.**

---

### 🟡 16. `name`이 적용 시엔 리터럴, 매칭 시엔 글로브

`segment.proto:13-14,28-29` · `dpb/path.go:19-49,57,81,90` · `navigate.go:65,149`

같은 스키마 필드 `name`이 **Delta 적용 시에는 리터럴 문자열**, **`Path` 매칭 시에는 글로브 패턴** 의미를 가지며, 정확히 `"**"`라는 문자열이 zero-or-more-segments 와일드카드로 예약되어 있다. 이스케이프 기제도, 판별자도 없다.

`segment.proto`는 *"Name of the field or key of the map entry."*라고만 말한다. 글로브 방언과 `**` 토큰은 **문서화되어 있다 — Go 안에서**(`dpb/path.go:19-21`, `:73-75`, 테스트 `dpb/path_test.go:48-83`). 와이어 포맷 소비자가 읽는 산출물에는 없다.

맵 키는 임의 문자열이므로 충돌이 도달 가능하다: `Field("*")`는 `PathEntry{Key:"anything"}`에 매치되므로, **합법적인 `map<string,X>` 키 `"*"`를 가리키는 Delta는 그 키에 정확히 적용되지만 임의 키용으로 작성된 필터에도 걸린다.** `[`를 포함한 키는 더 나쁘다 — `path.Match("a[b", "a[b")`는 `ErrBadPattern`과 함께 false를 반환하고, 세 호출 지점 모두 `ok, _ :=`로 오류를 버리므로 **그런 키는 진단 없이 매칭 불가능**하다.

**권고.** 한 필드가 두 의미를 지지 않게 할 것. 역할을 분리하거나(`Path`/`FieldSegment`는 순수 리터럴로 두고 매칭용 `PathPattern`/`PatternSegment` 메시지를 신설, 와일드카드 문법과 `**` 토큰을 `.proto`에 문서화) 명시적 판별자를 추가할 것(`oneof { string name = 1; string name_glob = 4; }`). 어느 쪽이든 방언과 인용 규칙을 `segment.proto`에 문서화하고 `ErrBadPattern`을 버리지 말고 표면화할 것.

---

### 🟡 17. `.proto`가 일곱 연산 중 하나도 문서화하지 않음

`delta.proto:14-32` (특히 `:15`, `:21`) · `value.proto` (주석 없음)

`delta.proto:21`은 문자 그대로 `  //` 한 줄이고, 22-31행이 `remove`·`test`·`insert`·`assign`·`move`·`copy`·`nest`를 **주석 없이** 선언한다. `value.proto`는 `Value`·`Struct`·`KeyValue` 어디에도 주석이 없다.

`.proto` 바깥에만 있는 규칙: `insert`의 presence 검사(README:206-213), `insert`의 `-1` append 규약(README:212), root assign = 컨테이너 전체 교체(README:277-295), root 연산 행렬(README:301-306), 변경 path의 must-already-exist 규칙(README:327-331), 숫자 세그먼트의 컨테이너별 해석(README:132-133), enum→`ValU` 규약(README:150), move/copy의 동일 컨테이너 제약(README:351).

게다가 `delta.proto:15`는 *"If no path nor targets are specified, the op applies to the root"*라고 하지만, 구현된 규칙은 ***"targets가 없으면 `path`가 도달한 컨테이너에 적용된다"***이다(`patch.go:144-153` + `message.go:13-15`). 이 일반화는 README:290-295에서 활용되고 `docs/root-replace-plan.md:33`에서 인정되었으나 **`.proto`는 갱신되지 않았다.**

**권고.** 규범 의미를 README에서 `delta.proto`·`value.proto`의 필드 주석으로 옮길 것: 일곱 kind 각각에 대해 메시지 필드/리스트 인덱스/맵 키/컨테이너 root에서의 효과, `insert`의 presence 규칙, 변경 path의 must-already-exist 규칙, proto 종류별 정규 `Value` arm. `delta.proto:15`를 *"If no targets are specified, the op applies to the container reached by `path` (the root message when `path` is empty)."*로 수정. **README를 명세가 아니라 파생 문서로 취급할 것.**

---

### 🟡 18. explicit presence 의존이 명시·고정되지 않음

`segment.proto:1,27-35,44-49` · `proto/sample/value.proto:5` · `map.go:352-357` · `navigate.go:64,147-149`

`FieldSegment.name`/`number`와 `RangeSegment.begin`/`end`는 **unset-vs-zero 구분이 있어야만 의미가 있고**, 그 구분은 edition 2023이 EXPLICIT presence를 기본값으로 하기 때문에만 존재한다. 스키마는 이 의존을 기록하지 않는다.

의존은 실제로 하중을 받고 있으며 Go 주석에만 적혀 있다:
```go
// A present name — even the empty string — is the string key.
// Only fall back to the number when no name was set at all.   (map.go:352-356)
```
`docs/root-replace-plan.md:117`이 이를 회귀 B1(빈 문자열 맵 키가 `"0"`으로 왕복)으로 기록하고 `HasName() && GetName() != ""` → `HasName()`으로 고쳤다. **그 수정은 정확히 한 곳에만 적용되었다** — `navigateMessage`는 여전히 옛 규약(`navigate.go:64`)이고, `navigateMap`은 presence 검사 없이 이름을 무조건 읽는다(`navigate.go:149`).

그리고 `proto/sample/value.proto:5`는 `option features.field_presence = IMPLICIT;`이다. **같은 한 줄을 `segment.proto`에 적용하면** 다섯 개 `Has*` 접근자가 전부 사라지고, `dpb/path.go:76-139`가 컴파일 오류를 내며, B1이 영구히 재도입된다.

**권고.** `proto/patch/segment.proto`에 `option features.field_presence = EXPLICIT;`를 추가할 것 — 오늘은 no-op이지만 **요구사항을 고정하고 기본값 변경에서 살아남는다.** `FieldSegment.name`과 `RangeSegment.begin`/`end`에 unset과 zero/empty가 구별되며 왜 그런지를 주석으로 명시. 그 다음 `navigateMessage`·`navigateMap`·`findFieldByFieldSegment`가 `map.go:354`의 `HasName()` 규약 하나를 공유하게 할 것.

---

### 🟡 19. `move`/`copy` 소스가 두 번째 위치를 지정할 수 없음

`delta.proto:27-28` · `segment.proto:27-35` · `dpb/json.go:19,152-167` · `message.go:79-85,107-114`

`Entry`에는 `path`가 하나뿐이므로 `move`/`copy` 소스는 `path`가 도달한 컨테이너에 대해 해석되는 단일 `FieldSegment`다. **컨테이너 간 재배치 — RFC 6902가 `from`에 완전한 포인터를 주는 이유 — 는 구조적으로 표현 불가능**하며, 다른 연산의 조합으로도 복구되지 않는다.

`dpb/json.go:158-165`는 따라서 `from`과 `path`의 부모가 다른 RFC 6902 move/copy를 거부해야 한다(`cross-container operations are not supported`). 즉 **`FromJsonPatch`는 유효한 입력에 대해 전역적이지 않다.**

자연스러운 우회는 조용히 성공하면서 아무것도 하지 않는다:
```go
Entry{path:[#111 m_1], targets:[#209], copy: FieldNum(109)}
// 소스를 m_1 *안에서* 해석 → fd_src == nil → return nil (message.go:107-111)
// err=nil, 메시지 무변화
```

타입도 모델의 나머지와 비대칭이다: `targets`는 `repeated Segment`(범위나 여러 위치)인데 소스는 단일 FieldSegment이므로, 리스트 소스는 `int(src.GetNumber())`로 퇴화한다(`list.go:154`) — `[a b c]`에서 `move: Field("b")`가 타겟 `[0]`과 함께 **조용히 원소 0에 작용**한다. 추가 제약(repeated/map 소스 거부)은 README:247 산문에만 있다.

**권고.** 맨 `FieldSegment` 소스를 완전한 위치로 교체:
```proto
message Location { Path path = 1; Segment segment = 2; }
```
두 arm이 함께 사용하고, `Location.path` 미설정은 호환을 위해 *"엔트리 path와 같은 컨테이너"*를 뜻하게 할 것. 이로써 리스트와 bool 키 맵에 대해 소스가 표현 가능해지고, `move`/`copy`가 미래 옵션을 실을 곳이 생기며, `FromJsonPatch`가 RFC 6902에 대해 전역이 되고, **소스가 엔트리 path 이전에 해석되는지 이후에 해석되는지**를 명시할 자리가 생긴다. 컨테이너 간 이동이 의도적으로 범위 밖이라면 `delta.proto`의 두 필드 옆에 repeated/map 소스 제약과 함께 그렇게 명시하고, **해석 불가한 소스를 `return nil`이 아니라 하드 오류로** 만들 것.

---

### 🟡 20. 버전 없는 `patch` 패키지 + 일반적 타입명

`delta.proto:3` · `segment.proto:3` · `value.proto:3`

스키마가 버전 없는 FQN `patch.Value`, `patch.Struct`, `patch.Path`, `patch.Segment`, `patch.Delta`, `patch.Entry`와 descriptor 경로 `patch/{value,segment,delta}.proto`를 점유한다. **같은 바이너리 안의 다른 patch/diff 스키마와 충돌이 그럴듯할 만큼 일반적이고, 파괴적 변경이 갈 곳이 없을 만큼 버전이 없다.**

Go의 `protoregistry.GlobalFiles`/`GlobalTypes`는 중복 파일 경로나 중복 FQN에서 init 시 panic하며, patchproto는 `protoregistry.GlobalTypes`를 기본값으로 쓴다(`patch.go:283-284`).

프로젝트 자신의 `buf.yaml`이 STANDARD를 선택하고 있고, `buf lint`가 이를 직접 보고한다:
```
proto/patch/delta.proto:3:1: Package name "patch" should be suffixed with a
correctly formed version, such as "patch.v1".
```
**스타일 취향이 아니라 프로젝트가 선언한 lint 정책이 실패하고 있는 것이다.**

**권고.** 외부 소비자가 생기기 전에 `package patch.v1;` + `proto/patch/v1/`로 이동할 것. descriptor 경로가 `patch/v1/delta.proto`가 되고, 본 검토가 요구하는 의미 수정을 위한 `patch.v2`가 확보된다.

---

### ⚪ 21. `bool remove = 3` — 아무것도 하지 않는 유효한 연산

`delta.proto:22-23` · `message.go:23-29,194-196` · `list.go:86-89,260-264` · `map.go:50-56,227-231`

oneof case가 이미 presence를 나르므로 bool 페이로드는 중복이다. 그리고 그 `false` 값은 **`WhichKind()`가 `remove`이면서 모든 백엔드·모든 컨테이너에서 성공을 보고하는 정의된 no-op**인 Entry를 만든다.

`delta.proto:23`은 일곱 kind 중 유일한 비-메시지 arm이며, 모든 소비자가 페이로드를 다시 검사한다(`message.go:24`, `list.go:87-89`, `map.go:51`, `message.go:195`, `list.go:261`, `map.go:228`).

검증: `Entry{targets:[SegName("s_1")], remove:false}`는 `WhichKind()==remove`, 와이어 `12050a03735f31 1800`(**oneof 태그가 값 0으로 와이어에 존재**), `s_1="hi"` 유지, `err=nil`. 반면 kind가 전혀 설정되지 않은 Entry는 올바르게 오류를 낸다(`unknown op: "not set"`). 즉 *"프로듀서가 remove를 선택했고 아무 의미도 두지 않았다"*와 *"프로듀서가 case를 영값으로 설정했다"*가 바이트 동일하다.

스칼라이므로 **수식자를 실을 수 없는 유일한 kind**이기도 하다. `remove`에 옵션("부재 시 실패", "X와 같을 때만 제거")을 추가하려면 새 kind 번호를 태워야 한다.

**권고.** `bool remove = 3;`을 빈 메시지 페이로드로 교체 (`Remove remove = 3;` + `message Remove {}`). 연산이 oneof case만으로 완전히 기술되고, `remove: false`가 표현 불가능해지며, 미래 수식자가 살 곳이 생긴다. **외부 소비자가 생기기 전이면 비용이 0이고, 이후면 kind 번호 하나다.**

---

## 6. 잘 설계된 부분

이 검토가 반대하지 않는, 그리고 보존해야 할 결정들.

- **패치 문서를 protobuf 메시지로 만든 것**(`delta.proto:10-32`)은 전제부터 옳고 구조적으로 잘 실행되었다. protobuf의 인코딩·툴링·스트리밍을 그대로 상속한다. **본 검토의 결함은 전부 "메시지가 말하지 않은 것"에 있지, 메시지가 되기로 한 결정에 있지 않다.**
- **`nest`**(`delta.proto:30`)는 JSON Patch의 평평한 포인터 모델보다 **실제로 더 나은 설계**다. 깊은 변경이 경로 접두사를 연산마다가 아니라 한 번만 나르고, 한 서브트리 아래의 관련 편집이 묶여 있으며, `Diff` 출력을 작게 만드는 것이 바로 이것이다. `patchproto/diff.go` 전체가 이를 중심으로 구성되어 있고 구조가 자연스럽게 읽힌다.
- **엔트리당 다중 `targets`**(`delta.proto:19`)는 잘 고른 확장이다. *"이 여러 필드에 같은 연산을"*은 실재하고 흔한 형태이며 RFC 6902는 이를 N개의 개별 연산으로 강제한다. **컬렉션 의미를 못박지 못한 것이 문제이지 아이디어 자체는 건전하다.**
- **`patchproto/cast.go`**는 나머지 코드가 따랐어야 할 모범이다. move/copy를 위한 명시적·열거적 변환 격자와 이름 붙은 `ErrInvalidCast` 오류 타입이 수치 폭, bool, string↔bytes, string→number 파싱을 실패 경로와 함께 다룬다. **#8이 요구하는 올바른 형태가 이미 저장소 안에 있다.**
- **`mapKeysFromSegments`**(`map.go:337-338`)는 미인식 `Segment` arm에 `unsupported segment kind for map target: %v`로 **fail-closed**한다. 이것이 올바른 전방 호환 동작이며, 다른 곳의 fail-open이 정책이 아니라 표류임을 증명한다.
- **`fieldSegmentToMapKey`**(`map.go:352-356`)는 *존재하는* 빈 이름을 빈 문자열 맵 키로 올바르게 취급하고 이름이 설정되지 않았을 때만 번호로 폴백하며, 이유를 주석으로 남긴다. `docs/root-replace-plan.md:117`이 이전 버그와 수정을 기록한다 — **protobuf presence에 대한 실제적이고 신중한 추론.**
- **`checkValueAssignable`**(`patch.go:177-204`)은 Set/Append panic과 nil 역참조를 오류로 바꾸기 위해 존재하며 doc 주석이 정확히 그렇게 말한다. 범주적 불일치(string→message, message→scalar, list→scalar, float→integer)를 올바르게 거부하고, **README의 "never a panic" 주장은 "rejected with an error" 주장이 성립하지 않는 곳에서도 유지된다.**
- **`Patched`**(`patch.go:66-73`)가 먼저 clone하므로 README 퀵스타트와 왕복 테스트가 쓰는 진입점은 호출 단위로 원자적이다.
- **`Diff`→`Patch` 왕복이 속성 테스트로 검증된다.** `diffRoundtrip`(`diff_test.go:11-21`)이 `proto.Equal(Patched(from, Diff(from,to)), to)`를 단언하고 메시지·리스트·맵 테스트 전반에 쓰인다.
- **`dpb.FromJsonPatch`가 표현할 수 없는 RFC 6902 케이스에 대해 시끄럽게 실패한다** (`cross-container operations are not supported`, `dpb/json.go:158-165`, `json_test.go:266-279`로 고정). 조용히 잘못 적용하지 않는다. **이 코드베이스의 다른 곳이 얼마나 fail-open인지를 생각하면 의도적이고 올바른 선택이다.**
- **`docs/root-replace-plan.md`는 정직한 설계 문서다.** 기존 결함(root 연산이 조용히 버려짐)을 식별하고, oneof kind 추가 대신 빈 targets 재사용을 택한 근거를 밝히고, 작업 범위를 명시하고, 알려진 한계를 숨기지 않고 기록한다(84행의 null 원소 누락, bool/정수 맵 키 커버리지 부재 포함).
- **edition 2023 + 기본 explicit presence 선택은 이 스키마에 옳다.** `Has*` 접근자가 존재하는 이유가 그것이고, 따라서 형식은 unset과 zero를 **구별할 수 있다.** `RangeSegment.Match`(`dpb/path.go:116-136`)는 이미 presence를 올바르게 읽는다 — **패처가 쓰지 않을 뿐 형식의 능력 자체는 온전하다.**
- **`Segment` oneof 번호(1, 2, 12, 13)와 `Entry.nest = 15`의 의도적 공백**은 필드 번호 수준에서 확장이 예상되었음을 보여준다 (미지의 arm 의미론은 예상되지 않았지만).

---

## 7. 권장 조치 순서

### 1단계 — 파괴적 실패를 멈춘다 (스키마 변경 없음)

fail-open을 fail-closed로 바꾸는 것만으로 데이터 손실 등급 결함 대부분이 잡힌 오류가 된다.

| 조치 | 대상 |
|---|---|
| `default:` arm 추가, 해석 불가 타겟을 오류로 | `messageFieldBySeg`, `expandListTargets` (#2) |
| `isClearValue`가 `Value_N_case`에만 true | `patch.go:350-352` (#5) |
| 해석 불가 `move`/`copy` 소스를 오류로 | `message.go:79-82, 107-111` (#6, #19) |
| 해석 불가 `Struct` 키를 오류로 | `patch.go:310-313` (#9) |
| 디코딩 불가 `Value` arm을 오류로 | `patchjson/patch.go:82-84` (#10) |
| root 분기를 **원본** `len(targets)`로 통일 | patchjson, patchstruct (#3) |
| `HasBegin()`/`HasEnd()`로 분기 | `expandListTargets` (#4) |
| 맵 키 해석기 통합 | `navigateMap` → `fieldSegmentToMapKey` (#11) |

### 2단계 — 스키마를 명세로 만든다 (하위 호환)

주석 추가와 `reserved` 선언은 와이어 호환성을 깨지 않는다.

- 일곱 kind 전부에 컨테이너별 효과를 규범 주석으로 (#17)
- `delta.proto:15`를 실제 규칙으로 수정 (#17)
- `RangeSegment`에 **단일 전역 규칙**을 쓰고 모순되는 예시 3, 4 삭제 (#4)
- `FieldSegment`가 선택자인지 제약인지 결정하고 명시 (#1)
- `targets`의 컬렉션 의미와 `move` 퇴화 사례 정의 (#6)
- 원자성 계약 명시 (#7)
- `Value` 캐리어별 정규 proto 종류 명시, enum → `i` 고정 (#8)
- `Struct` 키 유일성, `ListValue`의 null 원소, `test: null` 정의 (#12, #14, #15)
- `reserved 5, 6;` 등 빈 번호 예약 (#5)
- `option features.field_presence = EXPLICIT;` 고정 (#18)

### 3단계 — 구조적 수정 (`patch.v1`로 이주하며)

외부 소비자가 생기기 전이 유일한 기회다.

- `package patch.v1;` + `proto/patch/v1/` (#20)
- `Delta`에 `string message_type` 등 스키마 식별자 + `required_features` (#5, #9)
- root 표지를 명시적 필드로 (`bool at_container` 또는 `Targets` 래퍼) (#3)
- `Value`에 map/message 판별자 (#10)
- `KeyValue.key`를 전용 oneof로 (#13)
- `move`/`copy` 소스를 `Location`으로 (#19)
- `FieldSegment`에 oneof 규율, `insert`에 고유 append 앵커 (#11)
- `bool remove` → `message Remove {}` (#21)

---

## 8. 부록 — 기각된 지적

반증 담당이 검토했으나 **스키마 타당성 문제가 아니라고 판단**한 항목. 기록해 두는 이유는 다시 제기될 가능성이 높기 때문이다.

| 지적 | 기각 사유 |
|---|---|
| `Struct`의 repeated/map 인코딩이 명세되지 않음 | 명세는 존재한다 — `docs/root-replace-plan.md:53-56`이 규범적으로 정확히 기술하고 README:303-305가 반복하며 디코더가 강제한다(`patch.go:364-365, 382-383`). `diff.go`의 인코더는 그 확장 때 갱신되지 않은 **낡은 경로**일 뿐 (실질은 #10에 포함) |
| 스키마 없는 백엔드가 `Struct`/`ListValue`를 인코딩할 수 없음 | 인코딩은 가능하다 — JSON 객체는 `FieldSegment.name` 키를 가진 Struct로, 배열은 ListValue로 모호성 없이 대응된다. `Value.m`이 유일한 키 컨테이너 형태이므로 **인코딩 시점에는 descriptor가 불필요**하다. 해당 백엔드에 케이스가 없을 뿐 (디코딩 쪽 실질은 #10) |
| `Path.Match`가 필드를 번호로 매치하지 못함 | 동작은 재현되지만(`fieldSegmentToPathEntry`가 `PathEntryField`를 밀고 `Match`는 `PathEntryIndex`를 요구) **`PathEntry`/`PathEntryKind`는 `.proto`에 존재하지 않는 손으로 쓴 Go 타입**이다. `.proto` 변경 없이 고칠 수 있는 비규범 헬퍼의 버그이며, 스키마 수준 실질은 #1·#11에 이미 포함 |
| `FromJsonPatch`가 `test`를 조용히 버림 | 동작은 재현되지만 명세 수준에서 조용하지 않다 — `dpb/json.go:20`의 doc 주석과 `README.md:351-353`에 선언되어 있다. `.proto`는 RFC 6902를 언급하지 않으므로 **위반된 스키마 약속이 없다.** 선택적 interop 헬퍼의 문서화된 한계 |
| `reserved` 선언이 전무함 | 사실 기술은 정확하나 인과가 틀렸다. `reserved`는 **사용 후 제거된** 번호를 보호한다. 인용된 공백(Entry 9-14, Segment 3-11, Value 5-6)은 **한 번도 할당된 적이 없으므로** 미래 할당이 와이어 안전하다. 남는 실질(`Value` 5가 `google.protobuf.Value`와 충돌)은 #5에 포함 |
| `Diff`가 연산 표면의 소수만 발행함 | 중심 전제가 거짓 — `dpb.FromJsonPatch`가 두 번째 프로듀서로 `move`(`json.go:133`), `copy`(`json.go:148`), `path`를 발행한다. 남는 부분은 전부 #1-3, #6, #8, #11의 파생이며 권고는 테스트 커버리지 조언으로 환원됨 |

---

## 9. 검토 메타

- 대상 커밋: `434f8e0`
- 방법: 6개 독립 관점 감사 → 관점별 적대적 반증 → 중복 병합 및 종합 (에이전트 13개)
- 확인 52건 → 병합 21건 · 기각 6건
- 🔴 항목은 원본 코드에서 별도 재확인
