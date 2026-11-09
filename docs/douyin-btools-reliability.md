# 抖音开播检测的共享熔断问题

调查背景：[issue #1207](https://github.com/bililive-go/bililive-go/issues/1207)。报告涉及 bililive-go v0.8.2 与 biliLive-tools 3.1.2-bgo.2。

## 根因与修复位置

biliLive-tools 的 `/bgo/live-info` 调用全局 `APILoadBalancer`。旧实现把所有房间的失败累计到同一端点，达到阈值后仍允许此前在途的失败增加计数，并重新计算指数冷却。以默认参数计算，失败计数达到 17 时，冷却约为 14 小时 36 分钟。

端点到期后直接恢复全部请求，缺少单请求探测；一次查询也可能重复选择同一个端点。此外，BGO 路由没有传入 `uid`，`mobile`、`userHTML` 实际会回退到 `web`，不能把这三种选择视作三个独立上游。

直接取流 `/bgo/stream-info` 默认走 `web`，绕过上述共享状态。因此取流成功和开播检测因本地熔断失败可以同时出现。

根因修复必须进入 [biliLive-tools 源码](https://github.com/kira1928/biliLive-tools/tree/for-bgo/packages/DouYinRecorder/src/loadBalancer)，再构建新的工具包。本仓的错误传递改动能够独立兼容旧工具，但**仅升级 Go 程序不会修复旧工具包内的熔断状态机**。

配套依赖修复见 [biliLive-tools PR #2](https://github.com/kira1928/biliLive-tools/pull/2)，包括：

- 冷却固定且有上限，默认 3 分钟；熔断和手动重置推进状态代次，忽略旧请求对新状态的修改。
- 到期只允许一个恢复探测；探测成功才恢复普通请求。正常 `living:false` 同样清零连续失败计数。
- 一次查询去重，没有有效 `uid` 时排除会退回 `web` 的两个端点。
- BGO 检测和取流共用 12 秒总预算，调用方断开或超时后，把取消传到实际 Axios 请求；预算覆盖多端点回退。
- 返回稳定错误码、错误分类、端点、上游 HTTP 状态和可用时的下次探测时间。

## Go 侧诊断

非 200 响应最多读取 16 KiB 错误正文，仅输出经过验证的字段。正常的小错误响应仍会读到 EOF，以便复用 HTTP 连接；异常大正文不再无界排空。

| 诊断字段 | 含义 |
| --- | --- |
| `BGO_BALANCE_COOLDOWN` | 本地可用端点全部冷却 |
| `BGO_BALANCE_BUSY` | 恢复探测忙或没有启用的端点 |
| `BGO_BALANCE_UPSTREAM` / `BGO_UPSTREAM` | 本次上游查询失败 |
| `BGO_REQUEST_TIMEOUT` | 查询总预算耗尽 |
| `BGO_REQUEST_CANCELED` | 调用方取消 |
| `kind=http/timeout/parse/network/unknown` | 上游失败类别 |
| `api`、`upstream_status`、`retry_at` | 有效的端点、HTTP 状态、下次探测时间 |

旧工具只有 `error` 文本时，识别已知的全端点不可用、HTTP 444 等状态码、HTML 解析失败，以及 issue 中临时补丁的错误码。未知正文保留 HTTP 状态，不把原始异常消息放入日志或前端，防止泄漏 Cookie、签名 URL 和 HTML。

查询失败仍然返回错误，不能伪装成正常下播。Go 现有房间失败退避保持原有行为；此改动展示 `retry_at`，不据此改变调度策略。

## 发布与验证范围

依赖源码合并后，需要构建并发布新的 biliLive-tools 工具包，再更新工具分发配置。尚未发布的资产不能直接填入 `remote-tools-config.json`；当前配套修改不包含未经验证的新版本下载地址。

回归测试使用可控的并发请求、虚拟时间及本地 HTTP 服务，不依赖真实抖音房间。覆盖迟到失败、迟到成功、探测并发、重置隔离、取消、正常下播、端点去重和错误脱敏。

上游 444 的成因及 HTML 页面异常仍未确定。本次不把 issue 中的实验性队列和并发参数设为通用默认值，也不重新启用 Go 当前关闭的平台串行限流。大规模房间的稳态请求速率与长期漏检率仍需实际部署观察。
