# 진행 상황

[implementation-plan.md](implementation-plan.md)의 단계별 현황.

> 범례: ⬜ 대기 · 🟨 진행중 · ✅ 완료

| 단계 | 상태 | 패키지 |
|---|---|---|
| P0 에러 분류 | ✅ | `patch/errors.go` |
| P1 빌더 | ⬜ | `patch/` |
| P2 구조 검증 | ⬜ | `patch/` |
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
