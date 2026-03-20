# 常见问题

## 如何启用身份验证？

在 `config.yml` 中设置 `rpc.authentication` 配置项：

```yaml
rpc:
  authentication:
    enable: true
    web_username: admin
    web_password: your-password
    api_key: your-api-key
```

保存配置后重启程序即可生效。更多详情请参阅 [README.md](../README.md#身份验证)。

## 忘记了 Web 界面密码怎么办？

编辑 `config.yml` 文件，将 `rpc.authentication.enable` 设置为 `false` 或修改 `web_password` 为新密码，然后重启程序。

## 录制的视频频繁分段

可能是网络波动导致的。

## 录制的直播视频中途绿屏花屏

这通常是因为主播开始 pk 之后直播间的分辨率发生了微小的变化，而默认的 ffmpeg 程序无法处理这种分辨率变化导致的花屏。

在配置文件中启用 `use_native_flv_parser` 可以改为使用自制的 flv parser 录制视频。好处是遇到 pk 之类直播间分辨率变化的情况可以自动给视频分段来避免花瓶。坏处好像是遇到网络波动时视频更容易频繁分段？

## 快手录制不稳定

确实不稳定。快手网站对录播程序很严格...

## 程序崩溃啦

目前已知的崩溃方式有两种。

第一种会在直播间录制结束的瞬间崩溃，崩溃log参考 [这里](https://github.com/bililive-go/bililive-go/issues/383#issuecomment-1424675413)


第二种在录制开始的瞬间崩溃，崩溃 log 参考 [这里](https://github.com/bililive-go/bililive-go/issues/546)  
这种崩溃似乎是因为程序虽然找到了 ffmpeg 程序，但是在启动 ffmpeg 程序的时候发生了错误。有可能是你下载的 ffmpeg 程序不是你的电脑架构的可执行程序。你可以在命令行启动一下程序使用的 ffmpeg 看看有没有什么错误提醒。