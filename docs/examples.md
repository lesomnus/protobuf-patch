# 예제

`Patch`를 ProtoJSON으로 표현한 예제 모음. 모든 입출력은 실제로 적용해서 얻은 것이다.

대상 메시지는 `proto/sample/value.proto`의 `sample.Value`이고, 여기 쓰이는 필드는 이렇다:

```proto
string s_1 = 109;    string s_2 = 209;    string s_3 = 309;
int32  i32_1 = 105;  int64  i64_1 = 103;
Value  m_1 = 111;
repeated string r_s_1 = 1009;
map<string, string> m_s_s = 10909;
Closed closed_1 = 119;   // CLOSED enum: 0, 1, 2만 선언
```

---

## 0. 먼저 — ProtoJSON은 읽기용이지 전송용이 아니다

`Patch`는 **바이너리로 주고받아야 한다.** ProtoJSON을 거치면 스키마의 forward-compatibility 규칙이 무력해진다.

미래 개정판이 쓴 `Value` arm(예약된 필드 14)을 담은 문서를 ProtoJSON으로 찍으면:

```json
"assign": {
  "value": {}
}
```

**값이 사라진다.** ProtoJSON은 unknown field를 버리므로, 문서는 "값이 없는 assign"처럼 보인다. 바이너리로 받으면 그 필드가 남아 있고 검증이 잡아낸다:

```
delta.entries[0].assign.value: unknown field: a field from a newer revision,
or corrupt input; refusing rather than applying the part that is understood
```

`patch.proto`의 실패 계약이 이걸 명시한다 — *"A Patch therefore MUST NOT be carried over a transport that discards them."* 아래 예제들은 **사람이 읽기 위한** 표현이다.

> 참고: 64비트 필드는 ProtoJSON에서 문자열로 나온다. 아래 `Range`의 `"begin": "1"`이 그것이다.

---

## 1. 필드 하나 수정

가장 단순한 형태. `targets`가 어디에, `assign`이 무엇을 말한다.

```json
{
  "message_type": "sample.Value",
  "delta": {
    "entries": [
      {
        "targets": {
          "selectors": [
            { "key": { "field": { "name": "s_1" } } }
          ]
        },
        "assign": { "value": { "s": "new" } }
      }
    ]
  }
}
```

```
{"s_1":"old"}  →  {"s_1":"new"}
```

`"s"`는 문자열 필드를 위한 `Value` arm이다. arm은 대상 필드의 종류마다 정확히 하나씩 정해져 있다 — §3 참조.

## 2. 기본 연산

`entries[0]`의 모양만 바꾸면 나머지는 같으므로, 여기서는 그 부분과 결과만 적는다.

| 하려는 것 | 엔트리 | 결과 |
|---|---|---|
| 제거 | `"remove": {}` | `{"s_1":"x","s_2":"y"}` → `{"s_2":"y"}` |
| **번호로** 지목 | `"field": {"number": 109}` | `{"s_1":"old"}` → `{"s_1":"new"}` |
| 이름과 번호를 **함께 못박기** | `"field": {"name":"s_1","number":109}` | 같음. 스키마가 바뀌면 실패한다 (§4) |
| 여러 필드에 한 번에 | `selectors`에 셋 | `{}` → `{"s_1":"z","s_2":"z","s_3":"z"}` |
| 중첩 메시지로 내려가기 | `"path": {"segments":[{"field":{"name":"m_1"}}]}` | `{"m_1":{}}` → `{"m_1":{"s_1":"deep"}}` |
| 리스트 끝에 추가 | `selectors: [{"append":{}}]` + `insert` | `["a","b"]` → `["a","b","c"]` |
| 맵 항목 설정 | `"map_key": {"s":"b"}` + `assign` | `{"a":"1"}` → `{"a":"1","b":"2"}` |
| 컨테이너 전체 교체 | `"container": {}` + `assign` | `{"s_1":"x","s_2":"y"}` → `{"s_3":"only"}` |
| 값 옮기기 | `"move": {"from":{"same_container":{},"key":{...}}}` | `{"s_1":"v"}` → `{"s_2":"v"}` |

### 여러 대상에 한 번에

```json
"targets": {
  "selectors": [
    { "key": { "field": { "name": "s_1" } } },
    { "key": { "field": { "name": "s_2" } } },
    { "key": { "field": { "name": "s_3" } } }
  ]
},
"assign": { "value": { "s": "z" } }
```

`targets`는 **순서 없는 집합**이다. 셀렉터 순서를 바꿔도 결과가 같고, 같은 위치를 두 번 가리키면 오류다.

### 리스트의 구간

```json
"path": { "segments": [ { "field": { "name": "r_s_1" } } ] },
"targets": {
  "selectors": [ { "range": { "begin": "1", "end": "4" } } ]
},
"remove": {}
```

```
["a","b","c","d","e"]  →  ["a","e"]
```

`[begin, end)` 반열린 구간이다. 음수는 끝에서부터 세고, **한쪽을 비우면 열린다**:

| 범위 | 뜻 | 결과 |
|---|---|---|
| `{"begin":"1","end":"4"}` | `[1,4)` | `["a","e"]` |
| `{"begin":"-2"}` | 마지막 둘 | `["a","b","c"]` |
| `{}` | 전체 | `[]` |
| `{"begin":"0","end":"0"}` | **공집합** | 그대로 |

마지막 줄이 중요하다. `{"begin":"0","end":"0"}`과 `{"begin":"0"}`은 **다른 것**이다 — 전자는 아무것도 안 고르고 후자는 전부 고른다. 열림 여부를 값이 아니라 **필드 존재 여부**로 판단하기 때문이다.

### pre-entry 해석

```json
"targets": {
  "selectors": [
    { "key": { "index": "0" } },
    { "key": { "index": "2" } }
  ]
},
"insert": { "value": { "s": "Z" } }
```

```
["a","b","c"]  →  ["Z","a","b","Z","c"]
```

인덱스는 **엔트리가 시작하기 전** 상태로 해석된다. 첫 삽입이 둘째를 밀지 않는다.

### 조건부 적용

```json
"entries": [
  {
    "targets": { "selectors": [ { "key": { "field": { "name": "s_1" } } } ] },
    "test": { "value": { "s": "expected" } }
  },
  {
    "targets": { "selectors": [ { "key": { "field": { "name": "s_2" } } } ] },
    "assign": { "value": { "s": "written" } }
  }
]
```

```
{"s_1":"expected"}  →  {"s_1":"expected","s_2":"written"}
```

`test`가 성립하지 않으면 **문서 전체가 적용되지 않는다.** §4 첫 항목 참조.

### 중첩

```json
"targets": { "selectors": [ { "key": { "field": { "name": "m_1" } } } ] },
"nest": {
  "delta": {
    "entries": [
      { "targets": {"selectors":[{"key":{"field":{"name":"s_1"}}}]}, "assign": {"value":{"s":"a"}} },
      { "targets": {"selectors":[{"key":{"field":{"name":"s_2"}}}]}, "assign": {"value":{"s":"b"}} }
    ]
  }
}
```

```
{"m_1":{}}  →  {"m_1":{"s_1":"a","s_2":"b"}}
```

`path`가 매 엔트리에 접두사를 붙이는 것이라면, `nest`는 여러 엔트리가 **접두사를 공유**하게 한다.

---

## 3. 타입 캐스팅 — 없다

질문하신 것에 대한 답: **이 형식에는 타입 변환이 없다.** 의도된 설계다.

`Value`의 arm은 protobuf 타입 부류마다 정확히 하나씩 대응한다:

| 대상 필드 | arm |
|---|---|
| `bool` | `b` |
| `int32` `sint32` `sfixed32` | `i32` |
| `int64` `sint64` `sfixed64` | `i64` |
| `uint32` `fixed32` | `u32` |
| `uint64` `fixed64` | `u64` |
| `float` / `double` | `f32` / `f64` |
| `string` / `bytes` | `s` / `x` |
| `enum` | `e` |
| message / repeated / map | `m` / `l` / `map` |

맞지 않으면 **넓히지도 좁히지도 자르지도 않고 거부한다.**

```json
"targets": { "selectors": [ { "key": { "field": { "name": "i32_1" } } } ] },
"assign": { "value": { "s": "42" } }
```

```
delta.entries[0].assign.value: illegal arm for target:
  sample.Value.i32_1 takes i32, got s
```

**같은 부호의 정수끼리도 안 된다:**

```json
"assign": { "value": { "i64": "42" } }
```

```
delta.entries[0].assign.value: illegal arm for target:
  sample.Value.i32_1 takes i32, got i64
```

`move`/`copy`도 같다. 종류뿐 아니라 **선언된 타입**까지 일치해야 한다:

```
delta.entries[0].copy: type mismatch:
  source is string, target is int32; there is no conversion
```

### 왜 없나

구 구현에는 변환 격자(`cast.go`)가 있었고, 비대칭이고 문서화되지 않은 데다 **조용히 잘랐다** — `ValI(1<<40)`을 int32 필드에 쓰면 `0`이 되고 `err=nil`이었다. 저장·전송되는 패치 형식에서 무성 절단은 리뷰를 통과해서 살아남는 데이터 손실이다.

값이 자기 타입을 말하게 하면 **대상 스키마 없이도 검증**할 수 있고, 변환할 것이 없으니 절단될 것도 없다.

변환이 필요하면 **생산자 쪽에서** 하고 맞는 arm으로 써 보내면 된다. 형식이 대신 추측하지 않는다.

---

## 4. 에러

전부 실제 출력이다. **모든 실패는 대상을 건드리지 않고 끝난다** — 원자적이다.

### 실패한 `test`는 앞선 엔트리도 되돌린다

```json
"entries": [
  { "targets": {...s_1...}, "assign": { "value": { "s": "changed" } } },
  { "targets": {...i64_1...}, "test": { "value": { "i64": "42" } } }
]
```

```
입력  {"s_1":"orig"}
오류  delta.entries[1].test: test failed: at sample.Value.i64_1
이후  {"s_1":"orig"}          ← 첫 엔트리의 변경이 남지 않는다
```

### 없는 필드

```
delta.entries[0].targets.selectors[0]: target does not exist:
  nothing at that position in sample.Value; set on_missing to skip it deliberately
```

관용을 원하면 **문서에 적는다**:

```json
"on_missing": "ON_MISSING_SKIP",
"remove": {}
```

```
{"s_1":"x"}  →  {"s_1":"x"}     오류 없이 넘어감
```

관용은 **작성자의 선택**이지 리더의 기본값이 아니다. 그래서 와이어에 남는다.

### 이름과 번호가 어긋나면

```json
"field": { "name": "s_1", "number": 209 },
"on_missing": "ON_MISSING_SKIP"
```

```
delta.entries[0].targets.selectors[0].key.field: field identifiers disagree:
  field 209 of sample.Value is named "s_2", not "s_1"
```

**`on_missing`이 켜져 있어도 거부한다.** 이건 "없다"가 아니라 "이 문서는 다른 스키마를 대상으로 쓰였다"는 뜻이고, 형식이 가진 유일한 무결성 검사이기 때문이다. 저장해둔 패치가 필드 재번호 이후에도 조용히 성공하는 일을 막는다.

### 나머지

| 상황 | 오류 |
|---|---|
| `insert`인데 이미 값이 있음 | `target already has a value: sample.Value.s_1 is already set` |
| CLOSED enum이 선언하지 않은 값 | `undeclared enum value: sample.Closed does not declare 9` |
| 미래 개정판의 arm | `unknown field: ... refusing rather than applying the part that is understood` |

마지막 것이 핵심이다. **이해하지 못하는 문서는 아는 부분만 적용하는 대신 거부한다.**

---

## 5. 요약

- `Patch`는 **바이너리로** 주고받는다. ProtoJSON은 읽기용이다 (§0)
- `targets`는 **순서 없는 집합**, 인덱스는 **엔트리 시작 전** 상태 기준
- 범위의 열림은 값이 아니라 **필드 존재 여부**로 정해진다
- **타입 변환은 없다.** 맞지 않는 arm은 거부된다 (§3)
- **모든 실패는 대상을 건드리지 않는다**
- 관용은 `on_missing`으로 **문서에 적어야** 하고, 타입 오류·식별자 불일치·미지의 arm에는 적용되지 않는다
