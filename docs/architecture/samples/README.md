# Diagnostic Viewer 合成诊断包

本目录中的文件是为离线 Web Viewer 准备的**合成数据**，不来自真实用户，
也不包含真实房间号、URL、Cookie 或文件路径。它们的用途是：

1. 评审 `bililive.diagnostic-bundle/v1` 的最小可用 Schema；
2. 验证 Viewer 能否把“开始监控到 FLV 首字节”的耗时拆成不同阶段；
3. 验证同样是约 50 秒延迟时，Viewer 不会把不同根因混为一谈；
4. 作为前端规则引擎和回归测试的固定输入。

## 文件

| 文件 | 合成场景 | 预期根因 |
|---|---|---|
| `slow-ffmpeg-ready.json` | 已经发现开播，但首次启动时仍在下载和校验 FFmpeg（主示例） | `ffmpeg_async_initialization` |
| `slow-live-api-rate-limit.json` | 初次检测被平台级全局限流器排队 | `platform_rate_limiter_queue` |
| `slow-upstream-first-byte.json` | 检测正常，但三个 CDN 候选中前两个连接超时 | `stream_probe_fallback_timeout` |
| `complex-100-rooms-manual-restart.json` | 100 房间启动时手动停启目标房间，generation 交错并重新参与共享限流竞争 | `platform_shared_limiter_recompetition_after_generation_restart` |
| `bundle.schema.json` | 上述单文件诊断包的 JSON Schema | 不适用 |

前三个基础场景的共同表象都是：

```text
monitor.started ─────────────────────────────── segment.first_byte
0 ms                                                   50,000 ms
```

但阶段拆分完全不同：

```text
FFmpeg 初始化  检测 0.38s │ FFmpeg 等待 45.10s│ 其余阶段 4.52s
平台限流排队   检测 45.4s │ 取流与探测  3.60s │ parser/首字节 1.00s
流候选回退     检测 0.62s │ 取流与探测 48.38s │ parser/首字节 1.00s
```

这也是 Viewer 不应只显示一条“启动用了 50 秒”的原因。

复杂场景采用两个明确的时间口径：

```text
进程启动 → FLV 首字节             57,052 ms
generation 2 恢复监控 → FLV 首字节 50,000 ms
```

Viewer 应优先使用 `incident.anchor_start_event_id` 和
`incident.goal_event_id`，而不是猜测数组中的第一条 `monitor.started`。

## 顶层结构

```json
{
  "schema": "bililive.diagnostic-bundle/v1",
  "manifest": {},
  "configuration": {},
  "entities": [],
  "incident": {},
  "events": [],
  "metrics": [],
  "runtime_samples": [],
  "runtime_slices": [],
  "expected_analysis": {}
}
```

### 时间表达

- `manifest.time_origin` 是该次抓取的绝对时间原点；
- 事件使用从该原点起算的 `at_ms`，避免演示数据依赖浏览器时区；
- span 以共享 `span_id` 的 `.start`、`.end` 两条事件表示；
- `.end` 事件必须在 `outcome.duration_ms` 中给出按单调时钟计算的耗时；
- 指标的 `points` 使用 `[at_ms, value]`，便于纯前端直接绘图。

### 关联字段

| 字段 | 含义 |
|---|---|
| `run_id` | 一次程序运行 |
| `flow_id` | 一个直播间从检测、录制到后处理的业务因果链 |
| `span_id` | 一个有起止的操作 |
| `parent_span_id` | 形成瀑布图父子关系 |
| `links[].event_id` | 表达触发、重试、唤醒等非树形因果关系 |
| `entity_id` | 脱敏后的直播间、录制会话或工具 ID |
| `global_seq` | 进程内单 writer 的全局观察顺序；不表示并发任务的物理先后 |
| `task_id/goroutine_id` | 业务任务与执行它的 goroutine |
| `generation` | 同一房间停止、替换、恢复后的生命周期代次 |
| `dispatch_id/handler_id` | 一次派发与其中某个 handler 的身份 |

### 100 房间复杂样本

复杂样本不会为 100 个房间伪造海量事件：

- `manifest.room_population` 记录 100 个房间的覆盖方式；
- 目标房间完整记录；
- 5 个代表房间只记录低频完成摘要；
- 其余 94 个房间只进入 limiter 聚合指标；
- 规则引擎必须先按 `incident.anchor_entity_id` 和
  `incident.target_generation` 过滤，避免把其他房间的早期完成事件当成目标里程碑。

当前平台 limiter 不是可观察的 FIFO 队列，因此样本有意**不使用**
`queue_position`。可观测字段是：

```text
waiter_count_at_enter
grant_seq
recheck_count
total_wait_ms
fifo_guaranteed=false
```

合法结论是“generation 2 恢复后重新加入 100 房间共享 limiter 的竞争”，
不能写成“目标排在第 79 位”或“被放到 FIFO 队尾”。

停止 generation 1 时，`task.cancel.requested` 明确写出：

```text
cancel_target = listener.waiter.generation_1
does_not_cancel = wrapped_live.scheduler.shared_request
```

因此旧 scheduler-owned request 仍可能完成。样本中的
`scheduler.refresh.completed` 被标成 `stale_generation=true` 且
`recipient_count=0`，让 Viewer 能区分：

1. 用户操作是否很慢；
2. listener waiter 是否已经取消；
3. 底层共享请求为何仍在飞行；
4. 迟到结果是否错误地影响了 generation 2。

### Runtime 连续片段

复杂样本额外提供 `runtime_slices`。每条片段至少包含：

```text
goroutine_id / task_id / start_ms / end_ms
state / wait_reason / generation / flow_id / seq_on_g
```

`state` 取值为 `running/runnable/waiting/syscall`。同一 goroutine 只能按
`seq_on_g` 串行解释；不同 goroutine 之间只有在 `links` 明确给出
`caused_by` 或 `unblocked_by` 时才画因果箭头，不能单凭时间接近推断唤醒。

### 规则引擎应自己推导的里程碑

规则引擎不应依赖 `expected_analysis` 才能得出结论。该字段只是合成 fixture
的测试断言，真实用户诊断包可以完全没有它。

1. `monitor.started`：起点；
2. 第一个 `listener.poll.end` 且 `attrs.live=true`：确认开播；
3. `recorder.session.start`：RecorderManager 接受开播事件；
4. `stream.resolve.end`：获得直播流候选；
5. `stream.probe.end`：选到可用直播流；
6. `parser.start`：下载器开始工作；
7. `segment.first_byte`：FLV 文件第一次大于 0 字节。

推荐计算：

```text
检测耗时       = live_poll_end - monitor_started
事件交接耗时   = recorder_session_start - live_poll_end
录制准备耗时   = parser_start - recorder_session_start
首字节耗时     = first_byte - parser_start
总耗时         = first_byte - monitor_started
```

`incident.expected_detection_interval_ms` 是用户配置的轮询间隔，不是
“从开始监控到写出文件”的 SLA。若初始检测在一个间隔内完成，而大部分耗时发生在
`stream.probe` 或工具初始化阶段，Viewer 应明确排除“20 秒配置没有生效”。

## 连续开播的证据

重点样本除初次 `listener.poll.end` 外，还在约 20 秒和 40 秒处记录后续检查，
每次都有：

```json
{
  "name": "listener.poll.end",
  "attrs": {
    "live": true,
    "live_evidence": "platform_api"
  }
}
```

这只能证明“每次观测时平台 API 都返回开播”。Viewer 应写成
“抓取窗口内所有有效观测均为开播”，而不是声称拥有两个采样点之间的数学证明。

## 隐私约束

- 所有 ID 均为演示 ID；
- URL 只保留 `cdn-a.example.invalid` 之类不可访问的保留域名；
- `attrs` 中不得出现鉴权 query、Cookie、Authorization、主播真实昵称和宿主机绝对路径；
- 真正导出时应使用诊断包级随机盐对 room/file 等标识做 HMAC。
