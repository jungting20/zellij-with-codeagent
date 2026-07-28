# Pane 초기화 보장 설계

## 목표

`CreatePane`이 선택적으로 pane 생성, 입력 준비 대기, 초기 입력 전달을 하나의 runtime 연산으로 수행하게 한다. 초기 입력을 요청한 호출은 입력 전달까지 성공해야만 성공으로 간주한다.

ticket-manager는 이 연산을 사용해 coding-agent에 ticket 프롬프트가 전달된 뒤에만 worker slot을 `working`으로 전환한다. 초기화 실패 후 rollback이 완료되면 ticket을 안전하게 requeue하고, rollback이 불완전하면 기존 inspect/cleanup 복구 경로를 유지한다.

이 변경은 ticket-manager의 백그라운드 worker 시작 로직이므로 새 기본 role을 추가하지 않는다.

## 범위

다음 작업을 포함한다.

- runtime 및 transport `CreatePaneRequest`에 선택적인 초기 입력 필드를 추가한다.
- runtime이 준비 대기, generation 확인, 초기 입력 전달, 실패 rollback을 소유한다.
- transport가 초기화 실패와 부분 cleanup을 구분하는 오류 코드를 제공한다.
- execution plan이 별도 초기화 구현 대신 확장된 `CreatePane`을 사용한다.
- ticket-manager가 worker 생성과 프롬프트 전달을 단일 `CreatePane` 호출로 수행한다.
- 관련 runtime, transport, execution plan, ticket-manager 테스트를 갱신한다.

다음 작업은 포함하지 않는다.

- execution plan에 기존 탭 또는 anchor pane 타깃을 추가하지 않는다.
- ticket claim, 완료 marker 감지, worker 종료 또는 ticket 상태 전이 모델을 변경하지 않는다.
- 일반적인 pane 재시작 정책이나 retry 횟수를 새로 추가하지 않는다.
- runtime 경계를 우회해 ticket-manager에서 Zellij을 직접 호출하지 않는다.

## 공개 계약

runtime과 transport의 기존 `CreatePaneRequest`에 다음 필드를 추가한다.

```go
InitialInput          string
InitialInputReadyText string
```

transport JSON 이름은 각각 `initial_input`, `initial_input_ready_text`이며 빈 값은 생략한다.

동작 규칙은 다음과 같다.

1. `InitialInput`이 비어 있으면 기존 `CreatePane` 동작을 그대로 유지한다. 이 경우 `InitialInputReadyText`는 무시한다.
2. `InitialInput`이 있고 `InitialInputReadyText`가 비어 있으면 pane 등록 직후 generation을 확인하고 입력을 전달한다.
3. 두 값이 모두 있으면 runtime이 해당 pane의 화면 출력에 ready text가 포함될 때까지 기다린 뒤 입력을 전달한다.
4. 호출은 초기 입력 전달이 성공한 뒤에만 `CreatePaneResponse`를 반환한다.
5. 준비 대기와 입력 전달은 호출 context를 따르며, polling 간격은 기존 execution plan의 50ms 간격을 유지한다.
6. 동일한 pane ID와 동일한 요청의 동시 호출은 기존 create deduplication 안에서 하나의 생성 및 초기화 결과를 공유한다.
7. 동일한 pane ID에 다른 초기 입력 또는 ready text를 사용하면 기존의 다른 create 요청과 마찬가지로 충돌로 처리한다.

기존 요청에 새 필드가 없을 때의 응답 시점과 HTTP status는 변경하지 않는다.

## Runtime 처리 흐름

`Service.CreatePane`의 leader 호출은 target을 해석하고 실제 pane과 registry record를 생성한 뒤, 요청에 초기 입력이 있으면 공통 초기화 함수를 호출한다. follower 호출은 현재와 같이 leader가 초기화까지 마치고 `finishCreatePane`을 호출할 때까지 기다린다.

초기화 함수는 다음 순서를 지킨다.

1. 필요하면 `DumpScreen`을 polling해 ready text를 확인한다.
2. registry에서 logical pane을 다시 조회한다.
3. 조회한 record의 generation이 생성 직후 record와 같은지 확인한다.
4. 생성 직후 record가 가리키는 Zellij pane에 초기 입력을 전달한다.

generation이 달라졌거나 record가 사라진 경우 다른 pane에 입력하지 않고 초기화 실패로 처리한다.

초기화와 rollback 코드는 execution plan 전용 파일에 두지 않고 runtime의 pane 초기화 책임을 나타내는 작은 내부 단위로 분리한다. execution plan은 각 pane spec의 `InitialInput`과 `InitialInputReadyText`를 `CreatePaneRequest`에 전달하고 자체 polling 및 입력 전송 구현을 제거한다.

## Rollback과 오류 모델

준비 대기 또는 입력 전달이 실패하면 runtime은 요청 context의 취소 여부와 무관하게 최대 5초의 cleanup context를 생성해 방금 만든 pane을 rollback한다.

rollback은 다음 순서로 처리한다.

1. 생성된 실제 Zellij pane을 닫는다.
2. pane 종료가 성공한 경우 해당 generation의 output subscription을 중단한다.
3. 해당 generation의 registry record를 제거한다.

실제 pane 종료에 실패하면 logical identity를 이용한 후속 inspect/cleanup이 가능하도록 registry record를 제거하지 않는다. 실제 pane 종료 후 registry record 제거가 실패해도 부분 cleanup으로 보고 남은 record를 복구 정보로 유지한다. 이미 없거나 stale generation인 record는 rollback 완료로 간주한다.

오류는 다음 두 결과를 구분한다.

### `initialization_failed`

준비 대기 또는 입력 전달은 실패했지만 rollback이 완료되어 생성된 worker pane이 남아 있지 않다. runtime sentinel 오류는 원래 초기화 원인을 감싸며, transport는 전용 `initialization_failed` code를 반환한다. 이 code는 같은 ticket을 나중에 다시 시도할 수 있다는 의미에서 retryable로 표시한다.

ticket-manager는 이 오류를 안전한 생성 실패로 취급하고 별도 `ClosePane` 없이 ticket을 requeue한다.

### `cleanup_partial`

초기화 실패 후 실제 pane 종료 또는 registry 정리가 완료되지 않았다. 기존 `ErrCleanupPartial`과 transport `cleanup_partial` code를 사용하며, 오류가 초기화 실패와 cleanup 실패 원인을 함께 보존한다. 오류 매핑에서는 `cleanup_partial`을 `initialization_failed`보다 우선한다.

ticket-manager는 이 오류 또는 HTTP 응답 유실처럼 결과를 확정할 수 없는 오류를 `creationUncertain` 상태로 처리한다. 이후 runtime inspect로 worker 존재 여부를 확인하고, 존재하면 닫은 뒤 ticket을 requeue한다. 존재하지 않으면 동일한 전체 create 요청으로 재시도하므로 재시도에도 초기 입력 계약이 유지된다.

## Ticket-manager 변경

ticket-manager는 coding-agent worker 요청에 다음 값을 설정한다.

```go
InitialInput:          renderedPrompt + "\n"
InitialInputReadyText: "›"
```

기존의 `CreatePane` 이후 `SnapshotOutput` polling과 `SendInput` 호출은 worker 시작 경로에서 제거한다. `ManagerClient.SendInput`은 더 이상 필요하지 않으므로 interface와 fake 구현에서 제거한다. `SnapshotOutput`은 event stream 유실 시 completion marker를 복구하는 데 계속 사용한다.

worker 생성 호출에는 manager의 `StartupTimeout`으로 만든 context를 사용한다. 성공 응답을 받은 뒤에만 `paneCreated`를 기록하고 slot을 `managerSlotWorking`으로 전환한다.

오류 처리 규칙은 다음과 같다.

- `bad_request`, `not_found`, `initialization_failed`: pane이 남지 않는 안전한 실패로 처리하고 ticket을 requeue한다.
- `cleanup_partial`, 일반 runtime 오류, transport 단절 또는 timeout: 결과가 불확실한 실패로 처리하고 기존 inspect/cleanup 복구 경로로 전환한다.

ticket 완료 marker 처리, 완료 상태 전이, worker close 및 slot refill 흐름은 변경하지 않는다.

## Execution plan 변경

execution plan은 첫 pane과 나머지 동시 생성 pane 모두에 초기화 필드를 포함한 `CreatePaneRequest`를 전달한다. 각 `CreatePane`은 자신이 생성한 pane의 초기화 실패를 직접 rollback한다.

한 pane의 생성 또는 초기화가 실패하면 execution plan은 기존처럼 그 전에 성공한 다른 pane들을 rollback한다. 실패한 현재 pane은 이미 단일 pane rollback 대상이므로 plan 수준 rollback 목록에 중복으로 넣지 않는다. 여러 pane을 동시에 만들다가 일부만 성공한 경우 성공한 pane들만 plan 수준 rollback 대상으로 반환한다.

응답의 tab, pane 순서와 전체 plan의 all-or-nothing 성공 의미는 유지한다.

## 테스트 전략

### Runtime

- 초기 입력이 없을 때 `DumpScreen`과 `SendInput`을 호출하지 않고 기존 생성 결과를 반환한다.
- ready text가 여러 snapshot 뒤 나타나면 정확히 한 번 초기 입력을 전달한다.
- ready text가 비어 있으면 snapshot 없이 즉시 초기 입력을 전달한다.
- 입력 전달 실패 후 실제 pane 종료, subscription 중단, registry 제거를 확인한다.
- 준비 대기 timeout 후 취소되지 않은 cleanup context로 rollback하는지 확인한다.
- 실제 pane 종료가 실패하면 `ErrCleanupPartial`을 반환하고 registry record를 보존하는지 확인한다.
- 동일한 초기화 요청을 동시에 호출하면 실제 pane 생성과 초기 입력 전달이 각각 한 번인지 확인한다.
- 같은 pane ID에 다른 초기 입력을 사용하는 동시 또는 후속 요청이 충돌하는지 확인한다.

### Transport

- `initial_input`과 `initial_input_ready_text`가 JSON 요청에서 runtime 요청까지 손실 없이 변환되는지 확인한다.
- runtime 초기화 실패가 `initialization_failed` code로 변환되는지 확인한다.
- 초기화 실패와 `ErrCleanupPartial`이 함께 있으면 `cleanup_partial`이 우선하는지 확인한다.

### Execution plan

- 첫 pane과 나머지 pane의 초기 입력 전달 테스트를 유지한다.
- ready text 대기와 timeout rollback 테스트를 공통 `CreatePane` 초기화 경로 기준으로 유지한다.
- 한 pane 초기화 실패 시 현재 pane과 이전 성공 pane이 각각 한 번만 rollback되는지 확인한다.
- 나머지 pane 동시 생성과 응답 순서가 유지되는지 확인한다.

### Ticket-manager

- worker `CreatePaneRequest`에 rendered prompt의 trailing newline과 `›` ready text가 포함되는지 확인한다.
- 별도 `SendInput` 없이 create 성공 뒤 slot이 working 상태가 되는지 확인한다.
- `initialization_failed`에서 별도 close 없이 ticket을 requeue하는지 확인한다.
- `cleanup_partial`과 결과 불명 오류에서 기존 inspect/cleanup 복구가 동작하는지 확인한다.
- completion marker event 및 snapshot 복구 동작이 변경되지 않았는지 확인한다.

## 성공 기준

- ticket-manager는 프롬프트 전달 성공 전 worker를 시작된 것으로 기록하지 않는다.
- 초기화 실패와 rollback 성공이 함께 확인되면 ticket을 안전하게 requeue한다.
- rollback이 불완전하거나 응답 결과가 불명확하면 기존 복구 상태가 pane과 ticket의 중복 실행을 막는다.
- 초기 입력을 사용하지 않는 모든 기존 `CreatePane` 호출은 동작이 유지된다.
- execution plan과 ticket-manager가 동일한 runtime 초기화 구현을 사용한다.
- 관련 단위 테스트와 `go test ./...`가 통과한다.
