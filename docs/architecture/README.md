# bililive-go 架构与诊断

本目录目前包含三份维护者文档：

1. [异步生命周期导览](async-lifecycle.md)  
   从进程启动、Context/WaitGroup、事件派发、Live/Listener/Recorder/Pipeline，一直讲到关闭、热更新和事件交错。

2. [结构化业务轨迹与 Go Flight Recorder](structured-tracing-and-flight-recorder.md)  
   介绍已经落地的事件 Schema、因果 ID、滚动轨迹、Go 运行时黑盒、耐崩溃保存和后续演进路线。

3. [诊断轨迹 WebUI](diagnostic-viewer-webui.md)  
   介绍本机运行现场、异常退出调查、归档下载、浏览器本地分析、局域网手机访问，以及四种合成数据包。

> 业务轨迹、周期性 Go Flight Recorder、重启后异常运行发现、稳定调查包和
> `/api/diagnostics` 已接入。合成包仍保留为规则回归与 UI 演示数据。
